// Package peerlimit implements a Hook that limits the number of peers
// allowed per user (passkey) per torrent.
package peerlimit

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	yaml "gopkg.in/yaml.v2"

	"github.com/chihaya/chihaya/bittorrent"
	"github.com/chihaya/chihaya/middleware"
	"github.com/chihaya/chihaya/middleware/passkeyapproval"
	"github.com/chihaya/chihaya/pkg/log"
)

// Name is the name by which this middleware is registered with Chihaya.
const Name = "peer limit"

func init() {
	middleware.RegisterDriver(Name, driver{})
}

type driver struct{}

func (d driver) NewHook(optionBytes []byte) (middleware.Hook, error) {
	var cfg Config
	if err := yaml.Unmarshal(optionBytes, &cfg); err != nil {
		return nil, err
	}
	return NewHook(cfg)
}

// Config represents all the values required by this middleware.
type Config struct {
	RedisBroker         string        `yaml:"redis_broker"`
	LimitKeyPrefix      string        `yaml:"limit_key_prefix"`
	PeerLifetime        time.Duration `yaml:"peer_lifetime"`
	RedisReadTimeout    time.Duration `yaml:"redis_read_timeout"`
	RedisWriteTimeout   time.Duration `yaml:"redis_write_timeout"`
	RedisConnectTimeout time.Duration `yaml:"redis_connect_timeout"`
}

func (cfg Config) LogFields() log.Fields {
	return log.Fields{
		"name":                Name,
		"redisBroker":         cfg.RedisBroker,
		"limitKeyPrefix":      cfg.LimitKeyPrefix,
		"peerLifetime":        cfg.PeerLifetime,
		"redisReadTimeout":    cfg.RedisReadTimeout,
		"redisWriteTimeout":   cfg.RedisWriteTimeout,
		"redisConnectTimeout": cfg.RedisConnectTimeout,
	}
}

type hook struct {
	cfg  Config
	pool *redis.Pool
}

// NewHook returns an instance of the peer limit middleware.
func NewHook(cfg Config) (middleware.Hook, error) {
	if cfg.LimitKeyPrefix == "" {
		cfg.LimitKeyPrefix = "tracker:limit"
	}
	if cfg.PeerLifetime <= 0 {
		cfg.PeerLifetime = 30 * time.Minute
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

	h := &hook{
		cfg:  cfg,
		pool: p,
	}

	log.Info("peer limit middleware enabled", h.cfg)

	return h, nil
}

func (h *hook) HandleAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest, resp *bittorrent.AnnounceResponse) (context.Context, error) {
	// 如果没有配置 Redis，直接跳过
	if h.pool == nil {
		return ctx, nil
	}

	// 1. 获取 Passkey
	var passkey string
	if payload, ok := ctx.Value(passkeyapproval.PasskeyPayloadKey).(*passkeyapproval.Payload); ok && payload != nil && payload.Passkey != "" {
		passkey = payload.Passkey
	}
	if passkey == "" {
		passkey = routeParam(ctx, "passkey")
	}
	if passkey == "" {
		if v := req.Params; v != nil {
			if pk, ok := v.String("passkey"); ok {
				passkey = pk
			}
		}
	}

	// 如果未找到 Passkey（可能是公开 Tracker 或配置顺序问题），跳过检查
	if passkey == "" {
		return ctx, nil
	}

	ih := req.InfoHash.String()
	peerID := req.Peer.ID.String()

	// 构造 Key：tracker:limit:<passkey>:<infohash>
	key := fmt.Sprintf("%s:%s:%s", h.cfg.LimitKeyPrefix, passkey, ih)

	conn := h.pool.Get()
	defer conn.Close()

	if req.Event == bittorrent.Stopped {
		_, err := conn.Do("SREM", key, peerID)
		if err != nil {
			log.Error("peer limit: failed to remove peer", log.Err(err))
		}
		return ctx, nil
	}

	// 检查是否存在其他的 PeerID
	members, err := redis.Strings(conn.Do("SMEMBERS", key))
	if err != nil {
		log.Error("peer limit: failed to fetch members", log.Err(err))
		// Redis 故障时，默认放行
		return ctx, nil
	}

	for _, member := range members {
		if member != peerID {
			log.Info("peer limit: concurrent connection rejected", log.Fields{
				"passkey":      passkey,
				"infohash":     ih,
				"existingPeer": member,
				"newPeer":      peerID,
			})
			return ctx, bittorrent.ClientError("connection limit reached for this torrent")
		}
	}

	// 更新集合与过期时间
	conn.Send("MULTI")
	conn.Send("SADD", key, peerID)
	conn.Send("EXPIRE", key, int(h.cfg.PeerLifetime.Seconds()))
	_, err = conn.Do("EXEC")
	if err != nil {
		log.Error("peer limit: failed to update set", log.Err(err))
	}

	return ctx, nil
}

func (h *hook) HandleScrape(ctx context.Context, req *bittorrent.ScrapeRequest, resp *bittorrent.ScrapeResponse) (context.Context, error) {
	return ctx, nil
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

func routeParam(ctx context.Context, name string) string {
	rp, _ := ctx.Value(bittorrent.RouteParamsKey).(bittorrent.RouteParams)
	if rp == nil {
		return ""
	}
	return rp.ByName(name)
}
