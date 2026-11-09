package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

// ftpBackend talks FTP or FTPS (explicit AUTH TLS or implicit TLS). One FTP
// control connection handles one command at a time, so calls are serialised.
type ftpBackend struct {
	mu   sync.Mutex
	l    Location
	conn *ftp.ServerConn
	dirs map[string]bool // folders known to exist
}

func certFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// FormatFingerprint renders a hex fingerprint as AA:BB:CC.
func FormatFingerprint(hexFP string) string {
	var b strings.Builder
	for i := 0; i < len(hexFP); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hexFP[i:min(i+2, len(hexFP))])
	}
	return b.String()
}

func (b *ftpBackend) tlsConfig(trustErr *error) *tls.Config {
	// TLS 1.2: several FTP servers mishandle TLS 1.3 data connections and
	// silently truncate uploads. Uploads are size-checked as well.
	cfg := &tls.Config{ServerName: b.l.Host, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
	if b.l.TLSFingerprint == "" {
		// Normal certificate validation; on failure report the fingerprint so the
		// administrator can trust a self-signed certificate.
		cfg.InsecureSkipVerify = true
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no server certificate")
			}
			opts := x509.VerifyOptions{DNSName: b.l.Host, Intermediates: x509.NewCertPool()}
			for _, c := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(c)
			}
			if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
				fp := certFingerprint(cs.PeerCertificates[0])
				te := &TrustError{Kind: "tls", Fingerprint: FormatFingerprint(fp), Value: fp, Reason: err.Error()}
				*trustErr = te
				return te
			}
			return nil
		}
		return cfg
	}
	cfg.InsecureSkipVerify = true // replaced by the pinned fingerprint check below
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("no server certificate")
		}
		if fp := certFingerprint(cs.PeerCertificates[0]); fp != b.l.TLSFingerprint {
			te := &TrustError{Kind: "tls", Fingerprint: FormatFingerprint(fp), Value: fp, Mismatch: true}
			*trustErr = te
			return te
		}
		return nil
	}
	return cfg
}

func openFTP(ctx context.Context, l Location) (Backend, error) {
	b := &ftpBackend{l: l, dirs: map[string]bool{}}
	if err := b.connect(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *ftpBackend) connect(ctx context.Context) error {
	var trustErr error
	opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(30 * time.Second)}
	if b.l.Type == TypeFTPS {
		cfg := b.tlsConfig(&trustErr)
		if b.l.ImplicitTLS {
			opts = append(opts, ftp.DialWithTLS(cfg))
		} else {
			opts = append(opts, ftp.DialWithExplicitTLS(cfg))
		}
	}
	c, err := ftp.Dial(b.l.addr(), opts...)
	if trustErr != nil {
		return trustErr
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", b.l.addr(), err)
	}
	if err := c.Login(b.l.Username, b.l.Password); err != nil {
		c.Quit()
		return fmt.Errorf("login: %w", err)
	}
	b.conn = c
	return nil
}

// do runs fn, reconnecting once when the connection was dropped (servers
// close idle control connections).
func (b *ftpBackend) do(ctx context.Context, fn func(c *ftp.ServerConn) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		if err := b.connect(ctx); err != nil {
			return err
		}
	}
	err := fn(b.conn)
	if err != nil && isConnErr(err) {
		b.conn.Quit()
		b.conn = nil
		if cerr := b.connect(ctx); cerr != nil {
			return cerr
		}
		err = fn(b.conn)
	}
	return err
}

func isConnErr(err error) bool {
	var tp *textproto.Error
	if errors.As(err, &tp) {
		return tp.Code == ftp.StatusNotAvailable
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "connection reset")
}

func isNotFound(err error) bool {
	var tp *textproto.Error
	return errors.As(err, &tp) && (tp.Code == ftp.StatusFileUnavailable || tp.Code == ftp.StatusFileActionIgnored)
}

func (b *ftpBackend) full(name string) string { return "/" + join(b.l.Path, name) }

func (b *ftpBackend) mkdirs(c *ftp.ServerConn, dir string) {
	if dir == "/" || dir == "." || b.dirs[dir] {
		return
	}
	b.mkdirs(c, path.Dir(dir))
	_ = c.MakeDir(dir) // fails when it exists
	b.dirs[dir] = true
}

func (b *ftpBackend) Put(ctx context.Context, name string, data []byte) error {
	dst := b.full(name)
	return b.do(ctx, func(c *ftp.ServerConn) error {
		b.mkdirs(c, path.Dir(dst))
		tmp := dst + ".tmp"
		if err := c.Stor(tmp, bytes.NewReader(data)); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
		if n, err := c.FileSize(tmp); err == nil && n != int64(len(data)) {
			_ = c.Delete(tmp)
			return fmt.Errorf("upload %s: the server stored %d of %d bytes", name, n, len(data))
		}
		_ = c.Delete(dst)
		if err := c.Rename(tmp, dst); err != nil {
			return fmt.Errorf("rename %s: %w", name, err)
		}
		return nil
	})
}

func (b *ftpBackend) Get(ctx context.Context, name string) ([]byte, error) {
	var out []byte
	err := b.do(ctx, func(c *ftp.ServerConn) error {
		r, err := c.Retr(b.full(name))
		if err != nil {
			if isNotFound(err) {
				return fmt.Errorf("%s: %w", name, ErrNotExist)
			}
			return err
		}
		defer r.Close()
		out, err = io.ReadAll(r)
		return err
	})
	return out, err
}

func (b *ftpBackend) List(ctx context.Context, dir string) ([]Entry, error) {
	var out []Entry
	err := b.do(ctx, func(c *ftp.ServerConn) error {
		list, err := c.List(b.full(dir))
		if err != nil {
			// Servers answer a missing folder with 450, 550 or even 501.
			var tp *textproto.Error
			if isNotFound(err) || (errors.As(err, &tp) && (tp.Code == 450 || tp.Code == ftp.StatusBadArguments)) {
				return nil
			}
			return err
		}
		for _, e := range list {
			if e.Name == "." || e.Name == ".." {
				continue
			}
			switch e.Type {
			case ftp.EntryTypeFolder:
				out = append(out, Entry{Name: e.Name, Dir: true})
			case ftp.EntryTypeFile:
				out = append(out, Entry{Name: path.Base(e.Name), Size: int64(e.Size)})
			}
		}
		return nil
	})
	return out, err
}

func (b *ftpBackend) Delete(ctx context.Context, name string) error {
	return b.do(ctx, func(c *ftp.ServerConn) error {
		if err := c.Delete(b.full(name)); err != nil && !isNotFound(err) {
			return err
		}
		return nil
	})
}

func (b *ftpBackend) RemoveAll(ctx context.Context, dir string) error {
	if dir == "" {
		return errors.New("refusing to remove the location root")
	}
	return b.do(ctx, func(c *ftp.ServerConn) error {
		err := c.RemoveDirRecur(b.full(dir))
		if err != nil && isNotFound(err) {
			return nil
		}
		for d := range b.dirs {
			if strings.HasPrefix(d, b.full(dir)) {
				delete(b.dirs, d)
			}
		}
		return err
	})
}

func (b *ftpBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		err := b.conn.Quit()
		b.conn = nil
		return err
	}
	return nil
}
