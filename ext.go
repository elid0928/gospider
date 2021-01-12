package gospider

import (
	"context"
	"crypto/md5"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"github.com/slyrz/robots"
	"github.com/zhshch2002/goreq"
)

// WithDeduplicate 删除重复数据
// Hash标签去重
func WithDeduplicate() Extension {
	return func(s *Spider) {
		CrawledHash := map[[md5.Size]byte]struct{}{}
		lock := sync.Mutex{}
		s.OnTask(func(ctx *Context, t *Task) *Task {
			has := GetRequestHash(t.Req)
			lock.Lock()
			defer lock.Unlock()
			// 当CrawledHash中有 has时， 返回nil，
			if _, ok := CrawledHash[has]; ok {
				return nil
			}
			// 否则， 将has值作为键， 记录这个请求
			CrawledHash[has] = struct{}{}
			return t
		})
	}

}

// WithRobotsTxt 遵守Robots协议
func WithRobotsTxt(ua string) Extension {
	return func(s *Spider) {
		rs := map[string]*robots.Robots{}
		s.OnTask(func(ctx *Context, t *Task) *Task {
			var r *robots.Robots
			if a, ok := rs[t.Req.URL.Host]; ok {
				r = a
			} else {
				if u, err := t.Req.URL.Parse("/robots.txt"); err == nil {
					if resp, err := goreq.Get(u.String()).Do().Resp(); err == nil && resp.StatusCode == 200 {
						r = robots.New(strings.NewReader(resp.Text), ua)
						rs[t.Req.URL.Host] = r
					}
				}
			}
			if r != nil {
				if !r.Allow(t.Req.URL.Path) {
					return nil
				}
			}
			return t
		})
	}
}

// WithDepthLimit 爬取深度限制
func WithDepthLimit(max int) Extension {
	return func(s *Spider) {
		s.OnTask(func(ctx *Context, t *Task) *Task {
			// 当前请求为空或 当前请求上下文中记录的字段"depth" 为空时设置value的值为1
			if ctx.Req == nil || ctx.Req.Context().Value("depth") == nil {
				t.Req.Request = t.Req.WithContext(context.WithValue(t.Req.Context(), "depth", 1))
				return t
			}
			// 否则， 获取上下文中的"depth"值，
			depth := ctx.Req.Context().Value("depth").(int)
			// 判断 depth的值是否小于max
			if depth < max {
				// 当depth小于max值时，将depth +1，并保存
				t.Req.Request = t.Req.WithContext(context.WithValue(t.Req.Context(), "depth", depth+1))
				return t
			}
			// 否则， 返回空， 即爬取深度已达到最大值
			return nil

		})
	}
}

// WithMaxReqLimit 记录在内存中，进行同步
func WithMaxReqLimit(max int64) Extension {
	return func(s *Spider) {
		count := int64(0)
		s.OnTask(func(ctx *Context, t *Task) *Task {
			if count < max {
				atomic.AddInt64(&count, 1)
				return t
			}
			return nil
		})
	}
}

// WithErrorLog 打印errorlog
func WithErrorLog(f io.Writer) Extension {
	return func(s *Spider) {
		l := zerolog.New(f).With().Timestamp().Logger()
		send := func(ctx *Context, err error, t, stack string) {
			event := l.Err(err).
				Str("spider", s.Name).
				Str("type", "item").
				Str("ctx", fmt.Sprint(ctx)).
				Str("url", ctx.Req.URL.String()).
				AnErr("req err", ctx.Req.Err).
				AnErr("resp err", ctx.Resp.Err)
			if ctx.Resp != nil {
				event.Int("resp code", ctx.Resp.StatusCode)
				if ctx.Resp.Text != "" {
					event.Str("text", ctx.Resp.Text)
				}
			}
			event.Str("stack", SprintStack()).Send()
		}

		s.OnItem(func(ctx *Context, i interface{}) interface{} {
			if err, ok := i.(error); ok {
				send(ctx, err, "item", SprintStack())
			}
			return i
		})
		s.OnRecover(func(ctx *Context, err error) {
			send(ctx, err, "OnRecover", SprintStack())
		})
		s.OnReqError(func(ctx *Context, err error) {
			send(ctx, err, "OnReqError", SprintStack())
		})
		s.OnRespError(func(ctx *Context, err error) {
			send(ctx, err, "OnRespError", SprintStack())
		})
	}
}

// CsvItem Csv格式的数据
type CsvItem []string

// WithCsvItemSaver 将以csv格式保存
func WithCsvItemSaver(f io.Writer) Extension {
	lock := sync.Mutex{}
	w := csv.NewWriter(f)
	return func(s *Spider) {
		s.OnItem(func(ctx *Context, i interface{}) interface{} {
			if data, ok := i.(CsvItem); ok {
				lock.Lock()
				defer lock.Unlock()
				err := w.Write(data)
				if err != nil {
					log.Err(err).Msg("WithCsvItemSaver Error")
				}
				w.Flush()
			}
			return i
		})
	}
}
