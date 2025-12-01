package passkeyapproval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chihaya/chihaya/bittorrent"
	"github.com/stretchr/testify/assert"
)

func TestHandleAnnounce_HTTPValidation(t *testing.T) {
	// 1. Setup Mock HTTP Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request parameters
		passkey := r.URL.Query().Get("passkey")
		if passkey == "valid_passkey" {
			// Return nested JSON structure as expected by the fix
			resp := struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    struct {
					Valid bool `json:"valid"`
				} `json:"data"`
			}{
				Code:    1000,
				Message: "Success",
				Data: struct {
					Valid bool `json:"valid"`
				}{
					Valid: true,
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			resp := struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    struct {
					Valid bool `json:"valid"`
				} `json:"data"`
			}{
				Code:    1000,
				Message: "Success",
				Data: struct {
					Valid bool `json:"valid"`
				}{
					Valid: false,
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	// 2. Configure Hook
	cfg := Config{
		HTTPURL:     ts.URL,
		HTTPTimeout: 0, // Use default
	}
	h, err := NewHook(cfg)
	assert.NoError(t, err)

	// 3. Test Valid Passkey
	ctx := context.Background()
	req := &bittorrent.AnnounceRequest{
		Params: mockParams{
			params: map[string]string{
				"passkey": "valid_passkey",
			},
		},
	}

	newCtx, err := h.HandleAnnounce(ctx, req, nil)
	assert.NoError(t, err)
	assert.NotNil(t, newCtx)

	// 4. Test Invalid Passkey
	reqInvalid := &bittorrent.AnnounceRequest{
		Params: mockParams{
			params: map[string]string{
				"passkey": "invalid_passkey",
			},
		},
	}

	_, err = h.HandleAnnounce(ctx, reqInvalid, nil)
	assert.Equal(t, ErrUnapprovedPasskey, err)
}
