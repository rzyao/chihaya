// 流量推送中间件：将客户端 Announce 的上传/下载“增量”推送到 Redis Streams
// 用途：供 PT 站消费进行用户积分结算、风控与统计。
// 关键点：
// - 读取路由或查询中的 passkey 识别用户
// - 以 Hash 记录上次 uploaded/downloaded 与时间戳，计算本次增量（处理计数回绕）
// - 写入 Redis Streams 字段：用户与端点、du/dd 增量、left、event、ts/dt、interval/min_interval
// - 内置简单重试；建议 PT 侧用消费者组与幂等键聚合
package trafficpush

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

const Name = "traffic push"

func init() { middleware.RegisterDriver(Name, driver{}) }

type driver struct{}

func (d driver) NewHook(optionBytes []byte) (middleware.Hook, error) {
	// 读取 YAML 配置并初始化中间件
	var cfg Config
	if err := yaml.Unmarshal(optionBytes, &cfg); err != nil {
		return nil, err
	}
	return NewHook(cfg)
}

type Config struct {
	RedisBroker         string        `yaml:"redis_broker"`          // Redis 连接串，例如 redis://pwd@127.0.0.1:6379/0
	StreamKey           string        `yaml:"stream_key"`            // Streams 键名，默认 tracker:traffic
	LastKeyPrefix       string        `yaml:"last_key_prefix"`       // 上次计数 Hash 前缀，默认 tracker:last
	RetryCount          int           `yaml:"retry_count"`           // 推送失败重试次数
	RetryInterval       time.Duration `yaml:"retry_interval"`        // 推送失败重试间隔
	RedisReadTimeout    time.Duration `yaml:"redis_read_timeout"`    // 读取超时
	RedisWriteTimeout   time.Duration `yaml:"redis_write_timeout"`   // 写入超时
	RedisConnectTimeout time.Duration `yaml:"redis_connect_timeout"` // 连接超时
}

func (cfg Config) LogFields() log.Fields {
	return log.Fields{
		"name":                Name,
		"redisBroker":         cfg.RedisBroker,
		"streamKey":           cfg.StreamKey,
		"lastKeyPrefix":       cfg.LastKeyPrefix,
		"retryCount":          cfg.RetryCount,
		"retryInterval":       cfg.RetryInterval,
		"redisReadTimeout":    cfg.RedisReadTimeout,
		"redisWriteTimeout":   cfg.RedisWriteTimeout,
		"redisConnectTimeout": cfg.RedisConnectTimeout,
	}
}

type hook struct {
	cfg  Config
	pool *redis.Pool
}

func NewHook(cfg Config) (middleware.Hook, error) {
	// 校验与默认值填充
	if cfg.StreamKey == "" {
		cfg.StreamKey = "tracker:traffic"
	}
	if cfg.LastKeyPrefix == "" {
		cfg.LastKeyPrefix = "tracker:last"
	}
	if cfg.RetryCount <= 0 {
		cfg.RetryCount = 3
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = time.Second
	}

	ru, err := parseRedisURL(cfg.RedisBroker)
	if err != nil {
		return nil, err
	}

	// 创建 Redis 连接池（与存储实现保持一致的风格）
	p := &redis.Pool{
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

	h := &hook{cfg: cfg, pool: p}
	log.Info("traffic push middleware enabled", h.cfg)
	return h, nil
}

type redisURL struct {
	Host, Password string
	DB             int
}

func parseRedisURL(target string) (*redisURL, error) {
	// 解析 redis://[password@]host[/][db]
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

func (h *hook) HandleAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest, resp *bittorrent.AnnounceResponse) (context.Context, error) {
	// 1) 识别用户：优先从 context 获取（passkeyapproval 中间件已存储），其次从路由或查询参数
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

	// 2) 收集上下文字段与统计值
	ih := req.InfoHash.String()
	peerID := req.Peer.ID.String()
	port := req.Peer.Port
	ipStr := req.Peer.IP.String()
	af := req.Peer.IP.AddressFamily.String()
	event := req.Event.String()

	uploaded := req.Uploaded
	downloaded := req.Downloaded

	// 3) 上次计数键：last:<passkey>:<infohash>:<peer_id>
	lastKey := fmt.Sprintf("%s:%s:%s:%s", h.cfg.LastKeyPrefix, passkey, ih, peerID)

	conn := h.pool.Get()
	defer conn.Close()

	var lastUp, lastDown uint64
	var lastTs int64
	// 4) 读取上次 uploaded/downloaded/ts 以计算增量与时长
	if hmVals, err := redis.Values(conn.Do("HMGET", lastKey, "uploaded", "downloaded", "ts")); err == nil && len(hmVals) == 3 {
		_, _ = redis.Scan(hmVals, &lastUp, &lastDown, &lastTs)
	}

	du := uint64(0)
	dd := uint64(0)
	// 处理计数回绕：客户端重启导致计数变小，直接取当前值作为增量
	if uploaded >= lastUp {
		du = uploaded - lastUp
	} else {
		du = uploaded
	}
	if downloaded >= lastDown {
		dd = downloaded - lastDown
	} else {
		dd = downloaded
	}

	nowTs := time.Now().Unix()
	// 5) 更新上次计数快照（含端点与时间戳）
	_, _ = conn.Do("HMSET", lastKey, "uploaded", uploaded, "downloaded", downloaded, "port", port, "ip", ipStr, "af", af, "ts", nowTs)

	// 若无增量且事件为 none，则跳过写入，减少噪音
	if du == 0 && dd == 0 && req.Event == bittorrent.None {
		return ctx, nil
	}

	args := redis.Args{}.Add("XADD").Add(h.cfg.StreamKey).Add("*")
	// 6) 计算时长与响应建议间隔（秒）
	dt := int64(0)
	if lastTs > 0 {
		dt = nowTs - lastTs
	}
	intervalSec := int64(resp.Interval / time.Second)
	minIntervalSec := int64(resp.MinInterval / time.Second)
	// XADD 字段
	fields := []interface{}{
		"passkey", passkey,
		"infohash", ih,
		"peer_id", peerID,
		"port", port,
		"ip", ipStr,
		"af", af,
		"du", du,
		"dd", dd,
		"left", req.Left,
		"event", event,
		"ts", nowTs,
		"dt", dt,
		"interval", intervalSec,
		"min_interval", minIntervalSec,
	}

	if payload, ok := ctx.Value(passkeyapproval.PasskeyPayloadKey).(*passkeyapproval.Payload); ok {
		if payload.Fd != nil {
			fields = append(fields, "fd", fmt.Sprintf("%v", payload.Fd))
		}
		if payload.Pd != nil {
			fields = append(fields, "pd", fmt.Sprintf("%v", payload.Pd))
		}
	}

	args = args.AddFlat(fields)

	var err error
	// 7) 简单重试：最多 RetryCount 次；失败记录日志，保留上次快照供下次增量计算
	for i := 0; i < h.cfg.RetryCount; i++ {
		if _, err = conn.Do(args[0].(string), args[1:]...); err == nil {
			break
		}
		time.Sleep(h.cfg.RetryInterval)
	}
	if err != nil {
		log.Error("traffic push: XADD failed", log.Err(err))
	}

	return ctx, nil
}

func (h *hook) HandleScrape(ctx context.Context, _ *bittorrent.ScrapeRequest, _ *bittorrent.ScrapeResponse) (context.Context, error) {
	return ctx, nil
}

func routeParam(ctx context.Context, name string) string {
	// 从上下文获取命名路由参数（由 HTTP 前端注入）
	rp, _ := ctx.Value(bittorrent.RouteParamsKey).(bittorrent.RouteParams)
	if rp == nil {
		return ""
	}
	return rp.ByName(name)
}
