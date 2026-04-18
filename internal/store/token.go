package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Tokens are opaque to agents (spec §4.3). Internally they encode the
// (controller instance, pane id, absolute stream offset, mint time)
// tuple as base64url-encoded JSON. Base64 keeps them URL/shell-safe;
// JSON keeps them debuggable without the controller.
//
// The mint time is used by §9.6 quiescence to compute "pane has been
// idle since the checkpoint"; it is opaque to callers.
type tokenPayload struct {
	I string `json:"i"` // controller instance id
	P string `json:"p"` // pane id, e.g. "%42"
	O int64  `json:"o"` // stream offset
	T int64  `json:"t"` // mint time, unix nanoseconds
}

var (
	// ErrTokenMalformed is returned when a token fails to decode.
	ErrTokenMalformed = errors.New("token malformed")
	// ErrTokenWrongInstance is returned when a token was issued by a
	// different controller instance.
	ErrTokenWrongInstance = errors.New("token issued by a different controller instance")
	// ErrTokenWrongPane is returned when a token's pane id does not
	// match the request's pane id.
	ErrTokenWrongPane = errors.New("token is for a different pane")
	// ErrTokenEvicted is returned when a token's offset is below the
	// buffer's retained window (spec §11.8).
	ErrTokenEvicted = errors.New("token offset is no longer retained")
)

func encodeToken(instance string, pane domain.PaneID, offset int64, mintedAt time.Time) domain.Token {
	raw, _ := json.Marshal(tokenPayload{
		I: instance, P: string(pane), O: offset, T: mintedAt.UnixNano(),
	})
	return domain.Token(base64.RawURLEncoding.EncodeToString(raw))
}

func decodeToken(t domain.Token) (tokenPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(t))
	if err != nil {
		return tokenPayload{}, ErrTokenMalformed
	}
	var tp tokenPayload
	if err := json.Unmarshal(raw, &tp); err != nil {
		return tokenPayload{}, ErrTokenMalformed
	}
	return tp, nil
}
