package ratelimit

import "time"

// SetTimeout 调大单次查询的上限：超时会按设计放行，测试断言"被限流"时不能让机器负载决定结果。
func SetTimeout(s *RedisStore, d time.Duration) { s.timeout = d }
