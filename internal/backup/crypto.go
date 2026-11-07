package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/argon2"
)

// KDFParams are the Argon2id parameters that turn the backup passphrase into
// the key that wraps a repository's master key. Tests lower them.
var KDFParams = struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}{Time: 3, Memory: 64 * 1024, Threads: 4}

// ErrWrongPassphrase is returned when the passphrase does not open a repository.
var ErrWrongPassphrase = errors.New("wrong backup passphrase")

// ErrCorrupt is returned when stored data fails authentication.
var ErrCorrupt = errors.New("backup data is corrupt or was tampered with")

var magic = []byte("FWB1")

var (
	zenc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	zdec, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderConcurrency(0))
)

// keys are a repository's master keys: one encrypts objects, one derives
// chunk IDs (keyed, so the storage provider cannot test for known content).
type keys struct {
	aead  cipher.AEAD
	idKey []byte
}

func newKeys(master []byte) (*keys, error) {
	if len(master) != 64 {
		return nil, errors.New("master key must be 64 bytes")
	}
	block, err := aes.NewCipher(master[:32])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &keys{aead: aead, idKey: append([]byte(nil), master[32:]...)}, nil
}

func (k *keys) chunkID(p []byte) string {
	m := hmac.New(sha256.New, k.idKey)
	m.Write(p)
	return hex.EncodeToString(m.Sum(nil))
}

// seal compresses and encrypts an object. The object name is authenticated
// so objects cannot be swapped.
func (k *keys) seal(name string, plain []byte) ([]byte, error) {
	comp := zenc.EncodeAll(plain, make([]byte, 0, len(plain)/2+64))
	out := make([]byte, len(magic)+k.aead.NonceSize(), len(magic)+k.aead.NonceSize()+len(comp)+k.aead.Overhead())
	copy(out, magic)
	if _, err := rand.Read(out[len(magic):]); err != nil {
		return nil, err
	}
	nonce := out[len(magic):]
	return k.aead.Seal(out, nonce, comp, []byte(name)), nil
}

func (k *keys) open(name string, data []byte) ([]byte, error) {
	hdr := len(magic) + k.aead.NonceSize()
	if len(data) < hdr+k.aead.Overhead() || string(data[:len(magic)]) != string(magic) {
		return nil, ErrCorrupt
	}
	comp, err := k.aead.Open(nil, data[len(magic):hdr], data[hdr:], []byte(name))
	if err != nil {
		return nil, ErrCorrupt
	}
	plain, err := zdec.DecodeAll(comp, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return plain, nil
}

func deriveKEK(passphrase string, salt []byte, time, memory uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(passphrase), salt, time, memory, threads, 32)
}

func wrapKey(kek, master []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, master, []byte("fleetwarden-backup-master-key")), nil
}

func unwrapKey(kek, wrapped []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < aead.NonceSize() {
		return nil, ErrCorrupt
	}
	master, err := aead.Open(nil, wrapped[:aead.NonceSize()], wrapped[aead.NonceSize():], []byte("fleetwarden-backup-master-key"))
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	return master, nil
}
