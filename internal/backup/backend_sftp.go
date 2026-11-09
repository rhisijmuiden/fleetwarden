package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpBackend stores objects over SSH (SFTP). This is the "SCP" location
// type: every server that allows scp also offers SFTP, and SFTP supports
// listing, renaming and deleting, which backups and retention need.
type sftpBackend struct {
	l    Location
	ssh  *ssh.Client
	sftp *sftp.Client
}

// GenerateSSHKey creates an Ed25519 key pair for a location: the private key
// in OpenSSH format and the public key in authorized_keys format.
func GenerateSSHKey(comment string) (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, nil
}

// PublicKeyOf returns the authorized_keys line of a private key.
func PublicKeyOf(private string) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(private))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func openSFTP(ctx context.Context, l Location) (Backend, error) {
	var auths []ssh.AuthMethod
	if l.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(l.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if l.Password != "" {
		auths = append(auths, ssh.Password(l.Password),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = l.Password
				}
				return ans, nil
			}))
	}
	if len(auths) == 0 {
		return nil, errors.New("a password or private key is required")
	}
	var trustErr error
	cfg := &ssh.ClientConfig{
		User: l.Username,
		Auth: auths,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			if l.HostKey == "" {
				trustErr = &TrustError{Kind: "ssh", Fingerprint: ssh.FingerprintSHA256(key), Value: got, Reason: "first connection"}
				return trustErr
			}
			want, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l.HostKey))
			if err != nil || !bytes.Equal(want.Marshal(), key.Marshal()) {
				trustErr = &TrustError{Kind: "ssh", Fingerprint: ssh.FingerprintSHA256(key), Value: got, Mismatch: true}
				return trustErr
			}
			return nil
		},
		Timeout: 30 * time.Second,
	}
	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", l.addr())
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", l.addr(), err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, l.addr(), cfg)
	if trustErr != nil {
		conn.Close()
		return nil, trustErr
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh: %w", err)
	}
	client := ssh.NewClient(c, chans, reqs)
	sc, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("sftp: %w (the server must allow the SFTP subsystem)", err)
	}
	return &sftpBackend{l: l, ssh: client, sftp: sc}, nil
}

func (b *sftpBackend) full(name string) string {
	p := join(b.l.Path, name)
	if p == "" {
		return "."
	}
	return p
}

func notExist(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var se *sftp.StatusError
	return errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxNoSuchFile
}

func (b *sftpBackend) Put(_ context.Context, name string, data []byte) error {
	dst := b.full(name)
	if err := b.sftp.MkdirAll(path.Dir(dst)); err != nil {
		return fmt.Errorf("create folder: %w", err)
	}
	tmp := dst + ".tmp"
	f, err := b.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("upload %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	if err := b.sftp.PosixRename(tmp, dst); err != nil {
		_ = b.sftp.Remove(dst)
		if err := b.sftp.Rename(tmp, dst); err != nil {
			return fmt.Errorf("rename %s: %w", name, err)
		}
	}
	return nil
}

func (b *sftpBackend) Get(_ context.Context, name string) ([]byte, error) {
	f, err := b.sftp.Open(b.full(name))
	if err != nil {
		if notExist(err) {
			return nil, fmt.Errorf("%s: %w", name, ErrNotExist)
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (b *sftpBackend) List(_ context.Context, dir string) ([]Entry, error) {
	list, err := b.sftp.ReadDir(b.full(dir))
	if err != nil {
		if notExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(list))
	for _, fi := range list {
		out = append(out, Entry{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size()})
	}
	return out, nil
}

func (b *sftpBackend) Delete(_ context.Context, name string) error {
	if err := b.sftp.Remove(b.full(name)); err != nil && !notExist(err) {
		return err
	}
	return nil
}

func (b *sftpBackend) RemoveAll(_ context.Context, dir string) error {
	if dir == "" {
		return errors.New("refusing to remove the location root")
	}
	if err := b.sftp.RemoveAll(b.full(dir)); err != nil && !notExist(err) {
		return err
	}
	return nil
}

func (b *sftpBackend) Close() error {
	b.sftp.Close()
	return b.ssh.Close()
}
