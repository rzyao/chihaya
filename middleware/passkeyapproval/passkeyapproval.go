package passkeyapproval

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	yaml "gopkg.in/yaml.v2"

	"github.com/chihaya/chihaya/bittorrent"
	"github.com/chihaya/chihaya/middleware"
	"github.com/chihaya/chihaya/pkg/log"
)

type passkeyPayloadKey struct{}

var PasskeyPayloadKey = passkeyPayloadKey{}

const Name = "passkey approval"

func init() { middleware.RegisterDriver(Name, driver{}) }

type driver struct{}

func (d driver) NewHook(optionBytes []byte) (middleware.Hook, error) {
	var cfg Config
	if err := yaml.Unmarshal(optionBytes, &cfg); err != nil {
		return nil, err
	}
	return NewHook(cfg)
}

var (
	ErrMissingPasskey    = bittorrent.ClientError("missing passkey")
	ErrUnapprovedPasskey = bittorrent.ClientError("unapproved passkey")
	ErrInvalidPasskey    = bittorrent.ClientError("invalid passkey")
)

type Config struct {
	RedisBroker         string        `yaml:"redis_broker"`
	SetKey              string        `yaml:"set_key"`
	HTTPURL             string        `yaml:"http_url"`
	HTTPTimeout         time.Duration `yaml:"http_timeout"`
	HTTPAPIKeyHeader    string        `yaml:"http_api_key_header"`
	HTTPAPIKey          string        `yaml:"http_api_key"`
	CacheTTLSeconds     int           `yaml:"cache_ttl_seconds"`
	RedisReadTimeout    time.Duration `yaml:"redis_read_timeout"`
	RedisWriteTimeout   time.Duration `yaml:"redis_write_timeout"`
	RedisConnectTimeout time.Duration `yaml:"redis_connect_timeout"`
	EncryptionKey       string        `yaml:"encryption_key"`
}

func (cfg Config) LogFields() log.Fields {
	return log.Fields{
		"name":             Name,
		"redisBroker":      cfg.RedisBroker,
		"setKey":           cfg.SetKey,
		"httpURL":          cfg.HTTPURL,
		"httpTimeout":      cfg.HTTPTimeout,
		"httpAPIKeyHeader": cfg.HTTPAPIKeyHeader,
		"cacheTTLSeconds":  cfg.CacheTTLSeconds,
		"encryptionKey":    cfg.EncryptionKey != "",
	}
}

type hook struct {
	cfg        Config
	pool       *redis.Pool
	httpClient *http.Client
	aesGCM     cipher.AEAD
}

func NewHook(cfg Config) (middleware.Hook, error) {
	if cfg.SetKey == "" {
		cfg.SetKey = "pt:passkeys"
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 5 * time.Second
	}
	if cfg.HTTPAPIKeyHeader == "" {
		cfg.HTTPAPIKeyHeader = "X-API-Key"
	}

	var aead cipher.AEAD
	if cfg.EncryptionKey != "" {
		if len(cfg.EncryptionKey) != 32 {
			return nil, errors.New("encryption_key must be 32 bytes")
		}
		block, err := aes.NewCipher([]byte(cfg.EncryptionKey))
		if err != nil {
			return nil, err
		}
		aead, err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
	}

	var p *redis.Pool
	if cfg.RedisBroker != "" {
		ru, err := parseRedisURL(cfg.RedisBroker)
		if err != nil {
			return nil, err
		}
		p = &redis.Pool{
			MaxIdle:     3,
			IdleTimeout: 240 * time.Second,
			Dial: func() (redis.Conn, error) {
				opts := []redis.DialOption{
					redis.DialDatabase(ru.DB),
					redis.DialReadTimeout(cfg.RedisReadTimeout),
					redis.DialWriteTimeout(cfg.RedisWriteTimeout),
					redis.DialConnectTimeout(cfg.RedisConnectTimeout),
				}
				if ru.Password != "" {
					opts = append(opts, redis.DialPassword(ru.Password))
				}
				return redis.Dial("tcp", ru.Host, opts...)
			},
			TestOnBorrow: func(c redis.Conn, t time.Time) error {
				if time.Since(t) < 10*time.Second {
					return nil
				}
				_, err := c.Do("PING")
				return err
			},
		}
	}

	h := &hook{cfg: cfg, pool: p, httpClient: &http.Client{Timeout: cfg.HTTPTimeout}, aesGCM: aead}
	log.Info("passkey approval middleware enabled", h.cfg)
	return h, nil
}

type redisURL struct {
	Host, Password string
	DB             int
}

func parseRedisURL(target string) (*redisURL, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "redis" {
		return nil, fmt.Errorf("no redis scheme found")
	}
	db := 0
	parts := strings.Split(u.Path, "/")
	if len(parts) > 1 && parts[1] != "" {
		db, err = strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
	}
	return &redisURL{Host: u.Host, Password: u.User.String(), DB: db}, nil
}

type Payload struct {
	Passkey   string      `json:"pk"`
	Timestamp int64       `json:"ts"`
	Fd        interface{} `json:"fd,omitempty"`
	Pd        interface{} `json:"pd,omitempty"`
}

func (h *hook) decrypt(ciphertext string) (*Payload, error) {
	data, err := base64.URLEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}

	nonceSize := h.aesGCM.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := h.aesGCM.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return nil, err
	}

	var payload Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (h *hook) HandleAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest, resp *bittorrent.AnnounceResponse) (context.Context, error) {
	var passkey string

	if h.aesGCM != nil {
		// 1. Try "credential"
		ciphertext := routeParam(ctx, "credential")
		if ciphertext == "" {
			if v := req.Params; v != nil {
				if c, ok := v.String("credential"); ok {
					ciphertext = c
				}
			}
		}

		// 2. If no "credential", try "passkey" (backward compatibility)
		if ciphertext == "" {
			ciphertext = routeParam(ctx, "passkey")
			if ciphertext == "" {
				if v := req.Params; v != nil {
					if pk, ok := v.String("passkey"); ok {
						ciphertext = pk
					}
				}
			}
		}

		if ciphertext == "" {
			return ctx, ErrMissingPasskey
		}

		payload, err := h.decrypt(ciphertext)
		if err != nil {
			log.Error("failed to decrypt passkey", log.Fields{
				"err":        err,
				"ciphertext": ciphertext,
			})
			return ctx, ErrInvalidPasskey
		}
		passkey = payload.Passkey
		ctx = context.WithValue(ctx, PasskeyPayloadKey, payload)
	} else {
		// Encryption disabled, just read passkey
		passkey = routeParam(ctx, "passkey")
		if passkey == "" {
			if v := req.Params; v != nil {
				if pk, ok := v.String("passkey"); ok {
					passkey = pk
				}
			}
		}
		if passkey == "" {
			return ctx, ErrMissingPasskey
		}
	}

	if h.pool != nil {
		conn := h.pool.Get()
		defer conn.Close()
		ok, err := redis.Bool(conn.Do("SISMEMBER", h.cfg.SetKey, passkey))
		if err != nil {
			log.Error("failed to check passkey in redis", log.Fields{"err": err, "key": h.cfg.SetKey})
		}
		if err == nil && ok {
			log.Info("passkey found in redis", log.Fields{
				"key":      h.cfg.SetKey,
				"passkey":  passkey,
				"InfoHash": req.InfoHash.String(),
			})
			return ctx, nil
		}
		log.Info("passkey not found in redis", log.Fields{
			"key":      h.cfg.SetKey,
			"passkey":  passkey,
			"InfoHash": req.InfoHash.String(),
		})
	}

	if h.cfg.HTTPURL != "" {
		q := url.Values{}
		q.Set("passkey", passkey)
		u := h.cfg.HTTPURL
		if strings.Contains(u, "?") {
			u = u + "&" + q.Encode()
		} else {
			u = u + "?" + q.Encode()
		}
		log.Info("checking passkey with http api", log.Fields{
			"passkey":  passkey,
			"url":      u,
			"InfoHash": req.InfoHash.String(),
		})
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			log.Error("failed to create http request", log.Fields{"err": err, "url": u})
		} else {
			if h.cfg.HTTPAPIKey != "" {
				req.Header.Set(h.cfg.HTTPAPIKeyHeader, h.cfg.HTTPAPIKey)
			}
			r, err := h.httpClient.Do(req)
			if err != nil {
				log.Error("failed to perform http request", log.Fields{"err": err, "url": u})
			} else if r != nil {
				var vr struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Data    struct {
						Valid bool `json:"valid"`
					} `json:"data"`
				}
				bodyBytes, _ := io.ReadAll(r.Body)
				r.Body.Close()
				_ = json.Unmarshal(bodyBytes, &vr)
				log.Info("http validation result", log.Fields{"url": u, "status": r.StatusCode, "valid": vr.Data.Valid, "passkey": passkey, "body": string(bodyBytes)})
				if r.StatusCode/100 == 2 && vr.Data.Valid {
					if h.pool != nil && h.cfg.CacheTTLSeconds > 0 {
						conn := h.pool.Get()
						defer conn.Close()
						_, _ = conn.Do("SADD", h.cfg.SetKey, passkey)
						if h.cfg.CacheTTLSeconds > 0 {
							_, _ = conn.Do("EXPIRE", h.cfg.SetKey, h.cfg.CacheTTLSeconds)
						}
					}
					return ctx, nil
				} else if r.StatusCode/100 != 2 {
					log.Warn("http validation returned non-200 status", log.Fields{"status": r.StatusCode, "url": u})
				}
			}
		}
	}

	return ctx, ErrUnapprovedPasskey
}

func (h *hook) HandleScrape(ctx context.Context, req *bittorrent.ScrapeRequest, resp *bittorrent.ScrapeResponse) (context.Context, error) {
	return ctx, nil
}

func routeParam(ctx context.Context, name string) string {
	rp, _ := ctx.Value(bittorrent.RouteParamsKey).(bittorrent.RouteParams)
	if rp == nil {
		return ""
	}
	return rp.ByName(name)
}
