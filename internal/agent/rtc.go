package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"

	"github.com/rhisijmuiden/fleetwarden/internal/remote"
)

// The direct path of a remote desktop session: a WebRTC data channel
// between the agent and the operator's browser. The offer and answer travel
// over the encrypted relay, so the DTLS fingerprints are authenticated.
// Everything still works over the relay when the browser cannot reach the
// agent (no TURN server is used).

const (
	rtcGatherTimeout = 5 * time.Second
	rtcConnectWait   = 15 * time.Second
	rtcMaxBuffered   = 4 << 20 // stop sending until the channel drains below rtcLowBuffered
	rtcLowBuffered   = 1 << 20
)

// rtcLink is one data channel to the viewer.
type rtcLink struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel

	open   atomic.Bool
	closed chan struct{}
	once   sync.Once

	wmu  sync.Mutex
	low  chan struct{}
	rbuf []byte
}

// newRTCLink creates the peer connection and its data channel and returns
// the offer with all local candidates. onMsg gets whole session messages
// from the viewer; onOpen and onClose report the channel's state.
func newRTCLink(ctx context.Context, iceServers []string, onMsg func([]byte), onOpen, onClose func()) (*rtcLink, string, error) {
	var se webrtc.SettingEngine
	// The agent logs what matters itself; pion's own logging is noise
	// (for example about mDNS on hosts without IPv6).
	se.LoggerFactory = &logging.DefaultLoggerFactory{DefaultLogLevel: logging.LogLevelDisabled}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	cfg := webrtc.Configuration{}
	if len(iceServers) > 0 {
		cfg.ICEServers = []webrtc.ICEServer{{URLs: iceServers}}
	}
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, "", err
	}
	ordered := true
	dc, err := pc.CreateDataChannel("fleetwarden", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	l := &rtcLink{pc: pc, dc: dc, closed: make(chan struct{}), low: make(chan struct{}, 1)}
	dc.SetBufferedAmountLowThreshold(rtcLowBuffered)
	dc.OnBufferedAmountLow(func() {
		select {
		case l.low <- struct{}{}:
		default:
		}
	})
	dc.OnOpen(func() {
		l.open.Store(true)
		onOpen()
	})
	dc.OnClose(func() { l.shut(onClose) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if len(m.Data) == 0 {
			return
		}
		l.rbuf = append(l.rbuf, m.Data[1:]...)
		if m.Data[0] == remote.ChunkLast {
			msg := l.rbuf
			l.rbuf = nil
			onMsg(msg)
		} else if len(l.rbuf) > remote.MaxMessage {
			l.shut(onClose)
		}
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			l.shut(onClose)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, "", err
	}
	select {
	case <-gathered:
	case <-time.After(rtcGatherTimeout):
	case <-ctx.Done():
		pc.Close()
		return nil, "", ctx.Err()
	}
	return l, pc.LocalDescription().SDP, nil
}

// accept takes the viewer's answer. When the channel does not open in time
// the link closes itself.
func (l *rtcLink) accept(sdp string, onClose func()) error {
	if err := l.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return err
	}
	go func() {
		select {
		case <-l.closed:
		case <-time.After(rtcConnectWait):
			if !l.open.Load() {
				l.shut(onClose)
			}
		}
	}()
	return nil
}

var errRTCClosed = errors.New("direct path closed")

// send writes one session message in chunks. It waits while the browser
// is behind, like the relay socket does.
func (l *rtcLink) send(ctx context.Context, m []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	for off := 0; ; {
		if !l.open.Load() {
			return errRTCClosed
		}
		for l.dc.BufferedAmount() > rtcMaxBuffered {
			select {
			case <-l.low:
			case <-time.After(time.Second): // the event can be missed; look again
			case <-l.closed:
				return errRTCClosed
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		end := min(off+remote.ChunkSize, len(m))
		flag := remote.ChunkMore
		if end == len(m) {
			flag = remote.ChunkLast
		}
		chunk := make([]byte, 0, 1+end-off)
		chunk = append(append(chunk, flag), m[off:end]...)
		if err := l.dc.Send(chunk); err != nil {
			return err
		}
		off = end
		if flag == remote.ChunkLast {
			return nil
		}
	}
}

func (l *rtcLink) shut(onClose func()) {
	l.once.Do(func() {
		l.open.Store(false)
		close(l.closed)
		go l.pc.Close()
		if onClose != nil {
			onClose()
		}
	})
}

func (l *rtcLink) close() { l.shut(nil) }
