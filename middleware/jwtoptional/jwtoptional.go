package jwtoptional

import (
    "context"
    "crypto"
    "encoding/hex"
    "encoding/json"
    "errors"
    "net/http"
    "time"

    jc "github.com/SermoDigital/jose/crypto"
    "github.com/SermoDigital/jose/jws"
    "github.com/SermoDigital/jose/jwt"
    "github.com/mendsley/gojwk"
    yaml "gopkg.in/yaml.v2"

    "github.com/chihaya/chihaya/bittorrent"
    "github.com/chihaya/chihaya/middleware"
    "github.com/chihaya/chihaya/pkg/log"
    "github.com/chihaya/chihaya/pkg/stop"
)

const Name = "jwt optional"

func init() { middleware.RegisterDriver(Name, driver{}) }

type driver struct{}

func (d driver) NewHook(optionBytes []byte) (middleware.Hook, error) {
    var cfg Config
    if err := yaml.Unmarshal(optionBytes, &cfg); err != nil { return nil, err }
    return NewHook(cfg)
}

var (
    ErrMissingJWT  = bittorrent.ClientError("unapproved request: missing jwt")
    ErrInvalidJWT  = bittorrent.ClientError("unapproved request: invalid jwt")
)

type Config struct {
    Issuer            string        `yaml:"issuer"`
    Audience          string        `yaml:"audience"`
    JWKSetURL         string        `yaml:"jwk_set_url"`
    JWKUpdateInterval time.Duration `yaml:"jwk_set_update_interval"`
    RequireForSeeder  bool          `yaml:"require_for_seeder"`
    RequireForLeecher bool          `yaml:"require_for_leecher"`
}

func (cfg Config) LogFields() log.Fields {
    return log.Fields{
        "issuer": cfg.Issuer,
        "audience": cfg.Audience,
        "JWKSetURL": cfg.JWKSetURL,
        "JWKUpdateInterval": cfg.JWKUpdateInterval,
        "requireForSeeder": cfg.RequireForSeeder,
        "requireForLeecher": cfg.RequireForLeecher,
    }
}

type hook struct {
    cfg        Config
    publicKeys map[string]crypto.PublicKey
    closing    chan struct{}
}

func NewHook(cfg Config) (middleware.Hook, error) {
    h := &hook{cfg: cfg, publicKeys: map[string]crypto.PublicKey{}, closing: make(chan struct{})}
    if err := h.updateKeys(); err != nil { return nil, err }
    go func() {
        for {
            select {
            case <-h.closing:
                return
            case <-time.After(cfg.JWKUpdateInterval):
                _ = h.updateKeys()
            }
        }
    }()
    return h, nil
}

func (h *hook) updateKeys() error {
    resp, err := http.Get(h.cfg.JWKSetURL)
    if err != nil { log.Error("failed to fetch JWK Set", log.Err(err)); return err }
    var parsedJWKs gojwk.Key
    if err = json.NewDecoder(resp.Body).Decode(&parsedJWKs); err != nil { resp.Body.Close(); log.Error("failed to decode JWK JSON", log.Err(err)); return err }
    resp.Body.Close()
    keys := map[string]crypto.PublicKey{}
    for _, k := range parsedJWKs.Keys {
        pk, err := k.DecodePublicKey()
        if err != nil { log.Error("failed to decode JWK into public key", log.Err(err)); return err }
        keys[k.Kid] = pk
    }
    h.publicKeys = keys
    return nil
}

func (h *hook) Stop() stop.Result {
    select { case <-h.closing: return stop.AlreadyStopped; default: }
    c := make(stop.Channel)
    go func() { close(h.closing); c.Done() }()
    return c.Result()
}

func (h *hook) HandleAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest, resp *bittorrent.AnnounceResponse) (context.Context, error) {
    seeding := req.Left == 0 || req.Event == bittorrent.Completed
    require := (seeding && h.cfg.RequireForSeeder) || (!seeding && h.cfg.RequireForLeecher)
    if !require { return ctx, nil }
    if req.Params == nil { return ctx, ErrMissingJWT }
    jwtParam, ok := req.Params.String("jwt")
    if !ok { return ctx, ErrMissingJWT }
    if err := validateJWT(req.InfoHash, []byte(jwtParam), h.cfg.Issuer, h.cfg.Audience, h.publicKeys); err != nil { return ctx, ErrInvalidJWT }
    return ctx, nil
}

func (h *hook) HandleScrape(ctx context.Context, req *bittorrent.ScrapeRequest, resp *bittorrent.ScrapeResponse) (context.Context, error) { return ctx, nil }

func validateJWT(ih bittorrent.InfoHash, jwtBytes []byte, cfgIss, cfgAud string, publicKeys map[string]crypto.PublicKey) error {
    parsedJWT, err := jws.ParseJWT(jwtBytes)
    if err != nil { return err }
    claims := parsedJWT.Claims()
    if iss, ok := claims.Issuer(); !ok || iss != cfgIss { return jwt.ErrInvalidISSClaim }
    if auds, ok := claims.Audience(); !ok || !in(cfgAud, auds) { return jwt.ErrInvalidAUDClaim }
    ihHex := hex.EncodeToString(ih[:])
    if ihClaim, ok := claims.Get("infohash").(string); !ok || ihClaim != ihHex { return errors.New("invalid infohash claim") }
    parsedJWS := parsedJWT.(jws.JWS)
    kid, ok := parsedJWS.Protected().Get("kid").(string)
    if !ok { return errors.New("invalid kid") }
    publicKey, ok := publicKeys[kid]
    if !ok { return errors.New("unknown kid") }
    return parsedJWS.Verify(publicKey, jc.SigningMethodRS256)
}

func in(x string, xs []string) bool {
    for _, y := range xs {
        if x == y {
            return true
        }
    }
    return false
}
