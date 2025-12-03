package passkeyapproval

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/chihaya/chihaya/bittorrent"
	"github.com/stretchr/testify/assert"
)

// mockParams implements bittorrent.Params interface for testing
type mockParams struct {
	params map[string]string
}

func (m mockParams) String(key string) (string, bool) {
	v, ok := m.params[key]
	return v, ok
}

func (m mockParams) RawPath() string  { return "" }
func (m mockParams) RawQuery() string { return "" }

func TestEncryptDecrypt(t *testing.T) {
	key := "01234567890123456789012345678901" // 32 bytes
	cfg := Config{EncryptionKey: key}
	h, err := NewHook(cfg)
	assert.NoError(t, err)

	hookInstance := h.(*hook)

	// Encrypt
	pk := "mysecretpasskey"
	ts := int64(1234567890)
	payload := Payload{Passkey: pk, Timestamp: ts}
	payloadBytes, _ := json.Marshal(payload)

	block, _ := aes.NewCipher([]byte(key))
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)
	ciphertext := gcm.Seal(nonce, nonce, payloadBytes, nil)
	encoded := base64.URLEncoding.EncodeToString(ciphertext)

	// Decrypt
	decryptedPayload, err := hookInstance.decrypt(encoded)
	assert.NoError(t, err)
	assert.Equal(t, pk, decryptedPayload.Passkey)
	assert.Equal(t, ts, decryptedPayload.Timestamp)
}

func TestEncryptDecrypt_WithFdPd(t *testing.T) {
	key := "01234567890123456789012345678901" // 32 bytes
	cfg := Config{EncryptionKey: key}
	h, err := NewHook(cfg)
	assert.NoError(t, err)

	hookInstance := h.(*hook)

	// Encrypt
	pk := "mysecretpasskey"
	ts := int64(1234567890)
	payload := Payload{Passkey: pk, Timestamp: ts, Fd: true, Pd: "50%"}
	payloadBytes, _ := json.Marshal(payload)

	block, _ := aes.NewCipher([]byte(key))
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)
	ciphertext := gcm.Seal(nonce, nonce, payloadBytes, nil)
	encoded := base64.URLEncoding.EncodeToString(ciphertext)

	// Decrypt
	decryptedPayload, err := hookInstance.decrypt(encoded)
	assert.NoError(t, err)
	assert.Equal(t, pk, decryptedPayload.Passkey)
	assert.Equal(t, ts, decryptedPayload.Timestamp)
	assert.Equal(t, true, decryptedPayload.Fd)
	assert.Equal(t, "50%", decryptedPayload.Pd)
}

func TestHandleAnnounce_Encryption(t *testing.T) {
	key := "01234567890123456789012345678901"
	cfg := Config{EncryptionKey: key}
	h, err := NewHook(cfg)
	assert.NoError(t, err)

	// Helper to encrypt
	encrypt := func(pk string) string {
		payload := Payload{Passkey: pk, Timestamp: 1234567890}
		payloadBytes, _ := json.Marshal(payload)
		block, _ := aes.NewCipher([]byte(key))
		gcm, _ := cipher.NewGCM(block)
		nonce := make([]byte, gcm.NonceSize())
		io.ReadFull(rand.Reader, nonce)
		ciphertext := gcm.Seal(nonce, nonce, payloadBytes, nil)
		return base64.URLEncoding.EncodeToString(ciphertext)
	}

	// 1. Valid Encrypted Passkey (using credential param)
	ctx := context.Background()
	req := &bittorrent.AnnounceRequest{
		Params: mockParams{
			params: map[string]string{
				"credential": encrypt("validpasskey"),
			},
		},
	}

	// It should NOT return ErrInvalidPasskey. It will likely return ErrUnapprovedPasskey because we didn't mock Redis/HTTP.
	newCtx, err := h.HandleAnnounce(ctx, req, nil)
	assert.Equal(t, ErrUnapprovedPasskey, err)

	// Check context for payload
	payload, ok := newCtx.Value(PasskeyPayloadKey).(*Payload)
	assert.True(t, ok)
	assert.Equal(t, "validpasskey", payload.Passkey)

	// 2. Invalid Encrypted Passkey (Garbage in credential)
	req.Params = mockParams{
		params: map[string]string{
			"credential": "garbage",
		},
	}
	_, err = h.HandleAnnounce(ctx, req, nil)
	assert.Equal(t, ErrInvalidPasskey, err)

	// 3. Fallback to passkey param (should still work as ciphertext)
	req.Params = mockParams{
		params: map[string]string{
			"passkey": encrypt("validpasskey"),
		},
	}
	_, err = h.HandleAnnounce(ctx, req, nil)
	assert.Equal(t, ErrUnapprovedPasskey, err)

	// 4. Plaintext Passkey in credential (Strict Mode)
	req.Params = mockParams{
		params: map[string]string{
			"credential": "plaintextpasskey",
		},
	}
	_, err = h.HandleAnnounce(ctx, req, nil)
	assert.Equal(t, ErrInvalidPasskey, err)
}

func TestHandleAnnounce_NoEncryption(t *testing.T) {
	cfg := Config{} // No key
	h, err := NewHook(cfg)
	assert.NoError(t, err)

	ctx := context.Background()
	req := &bittorrent.AnnounceRequest{
		Params: mockParams{
			params: map[string]string{
				"passkey": "plaintextpasskey",
			},
		},
	}

	// Should proceed to check (returns ErrUnapprovedPasskey because no Redis)
	_, err = h.HandleAnnounce(ctx, req, nil)
	assert.Equal(t, ErrUnapprovedPasskey, err)
}
