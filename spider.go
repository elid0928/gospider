package gospider

import (
	"errors"
	"fmt"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/tidwall/gjson"
	"github.com/zhshch2002/goreq"
)

var (
	// UnknownExt 新错误
	UnknownExt = errors.New("unknown ext")
)

// Handler 为func(ctx *Context)类型
type Handler func(ctx *Context)

// Extension 为func(s *Spider)类型
type Extension func(s *Spider)

// Task 类
type Task struct {
	Req      *goreq.Request
	Handlers []Handler
	Meta     map[string]interface{}
}

// Item 类型
// 包含一个上下文Context和数据Data
type Item struct {
	Ctx  *Context
	Data interface{}
}

// NewTask 工厂方法，
func NewTask(req *goreq.Request, meta map[string]interface{}, a ...Handler) (t *Task) {
	t = &Task{
		Req:      req,
		Handlers: a,
		Meta:     meta,
	}
	return
}

// Spider 爬虫本体
type Spider struct {
	Name    string
	Logging bool // 日志开启标志 ， true为开启， false为关闭

	Client *goreq.Client // http客户端
	Status *SpiderStatus // 爬虫状态类型
	wg     sync.WaitGroup

	onTaskHandlers      []func(ctx *Context, t *Task) *Task             // handler方法集合(func(ctx *Context, t *Task) *Task)
	onRespHandlers      []Handler                                       // func(ctx *Context) 集合，  没有返回值
	onItemHandlers      []func(ctx *Context, i interface{}) interface{} // 因为不知道Item的数据类型， 所以接收任意类型的数据， 并返回
	onRecoverHandlers   []func(ctx *Context, err error)                 // 错误(panic)捕捉模式下的处理方法
	onReqErrorHandlers  []func(ctx *Context, err error)                 // 请求错误后的处理方法
	onRespErrorHandlers []func(ctx *Context, err error)                 // 响应错误后的处理方法
}

// NewSpider 创建Spider的工厂类
func NewSpider(e ...interface{}) *Spider {
	s := &Spider{
		Name:    "spider",
		Logging: true,
		Client:  goreq.NewClient(),
		Status:  NewSpiderStatus(),
	}
	s.SetWaitGroup()
	s.Use(e...)
	return s
}

// SetWaitGroup 设置waitgroup
func (s *Spider) SetWaitGroup() {
	s.wg = sync.WaitGroup{}
}

// Use 类型转换
// 即NewSpider接收各类型的方法，这些方法与如下case中一致的话，就是用s作为传参执行
// 相当于自定义初始化
func (s *Spider) Use(exts ...interface{}) {
	// 类型转换
	for _, fn := range exts {
		switch fn.(type) {
		case func(s *Spider):
			fn.(func(s *Spider))(s)
			break
		case Extension:
			fn.(Extension)(s)
			break
		case goreq.Middleware, func(*goreq.Client, goreq.Handler) goreq.Handler:
			s.Client.Use(fn.(goreq.Middleware))
			break
		default:
			panic(UnknownExt)
		}
	}
}

func (s *Spider) Forever() {
	select {}
}

// Wait 内置WaitGroup，调用wait方法
func (s *Spider) Wait() {
	s.wg.Wait()
}

// 处理任务
func (s *Spider) handleTask(t *Task) {
	s.Status.FinishTask()
	ctx := &Context{
		s:     s,
		Req:   t.Req,
		Resp:  nil,
		Meta:  t.Meta,
		abort: false,
	}
	// 相当于 final， 错误捕捉 panic级别
	defer func() {
		// recover catch panic？,能让程序不退出继续执行
		if err := recover(); err != nil {
			if s.Logging {
				log.Error().Err(fmt.Errorf("%v", err)).Str("spider", s.Name).Str("context", fmt.Sprint(ctx)).Str("stack", SprintStack()).Msg("handler recover from panic")
			}
			if e, ok := err.(error); ok {
				s.handleOnError(ctx, e)
			} else {
				s.handleOnError(ctx, fmt.Errorf("%v", err))
			}
		}
	}()
	if t.Req.Err != nil {
		if s.Logging {
			log.Error().Err(fmt.Errorf("%v", ctx.Req.Err)).Str("spider", s.Name).Str("context", fmt.Sprint(ctx)).Str("stack", SprintStack()).Msg("req error")
		}
		s.handleOnReqError(ctx, t.Req.Err)
		return
	}
	ctx.Resp = s.Client.Do(t.Req)
	if ctx.Resp.Err != nil {
		if s.Logging {
			log.Error().Err(fmt.Errorf("%v", ctx.Resp.Err)).Str("spider", s.Name).Str("context", fmt.Sprint(ctx)).Str("stack", SprintStack()).Msg("resp error")
		}
		s.handleOnRespError(ctx, ctx.Resp.Err)
		return
	}
	if s.Logging {
		log.Debug().Str("Spider", s.Name).Str("context", fmt.Sprint(ctx)).Msg("Finish")

	}
	s.handleOnResp(ctx)
	if ctx.IsAborted() {
		return
	}
	for _, fn := range t.Handlers {
		fn(ctx) // 执行传入的处理方法
		if ctx.IsAborted() {
			return
		}
	}
}

// SeedTask  种子任务
// 初始化context， 并将请求加入到Task中， 即AddTask
func (s *Spider) SeedTask(req *goreq.Request, h ...Handler) {
	ctx := &Context{
		s:     s,
		Req:   nil,
		Resp:  nil,
		Meta:  map[string]interface{}{},
		abort: false,
	}
	ctx.AddTask(req, h...)
}

func (s *Spider) addTask(t *Task) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleTask(t)
	}()
	s.Status.AddTask()
}

func (s *Spider) addItem(i *Item) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleOnItem(i)
	}()
	s.Status.AddItem()
}

// OnTask 任务
// 将要在任务中的执行的方法添加到onTaskHandlers中， 仅接收func(ctx *Context, t *Task) * Task的类型
/*************************************************************************************/
func (s *Spider) OnTask(fn func(ctx *Context, t *Task) *Task) {
	s.onTaskHandlers = append(s.onTaskHandlers, fn)
}

// 执行onTaskHandlers中的方法
func (s *Spider) handleOnTask(ctx *Context, t *Task) *Task {
	for _, fn := range s.onTaskHandlers {
		t = fn(ctx, t)
		if t == nil {
			return t
		}
	}
	return t
}

// OnResp 响应处理方法
/*************************************************************************************/
func (s *Spider) OnResp(fn Handler) {
	s.onRespHandlers = append(s.onRespHandlers, fn)
}

// OnHTML html文件处理
func (s *Spider) OnHTML(selector string, fn func(ctx *Context, sel *goquery.Selection)) {
	s.OnResp(func(ctx *Context) {
		if ctx.Resp.IsHTML() {
			if h, err := ctx.Resp.HTML(); err == nil {
				h.Find(selector).Each(func(i int, selection *goquery.Selection) {
					fn(ctx, selection)
				})
			}
		}
	})
}

// OnJSON json文件处理
func (s *Spider) OnJSON(q string, fn func(ctx *Context, j gjson.Result)) {
	s.onRespHandlers = append(s.onRespHandlers, func(ctx *Context) {
		if ctx.Resp.IsJSON() {
			if j, err := ctx.Resp.JSON(); err == nil {
				if res := j.Get(q); res.Exists() {
					fn(ctx, res)
				}
			}
		}
	})
}
func (s *Spider) handleOnResp(ctx *Context) {
	for _, fn := range s.onRespHandlers {
		if ctx.IsAborted() {
			return
		}
		fn(ctx)
	}
}

// OnItem 处理
/*************************************************************************************/
func (s *Spider) OnItem(fn func(ctx *Context, i interface{}) interface{}) {
	s.onItemHandlers = append(s.onItemHandlers, fn)
}
func (s *Spider) handleOnItem(i *Item) {
	defer func() {
		if err := recover(); err != nil {
			if s.Logging {
				log.Error().Err(fmt.Errorf("%v", err)).Str("spider", s.Name).Str("context", fmt.Sprint(i.Ctx)).Str("stack", SprintStack()).Msg("OnItem recover from panic")
			}
			if e, ok := err.(error); ok {
				s.handleOnError(i.Ctx, e)
			} else {
				s.handleOnError(i.Ctx, fmt.Errorf("%v", err))
			}
		}
	}()
	for _, fn := range s.onItemHandlers {
		i.Data = fn(i.Ctx, i.Data)
		if i.Data == nil {
			return
		}
	}
}

/*************************************************************************************/
func (s *Spider) OnRecover(fn func(ctx *Context, err error)) {
	s.onRecoverHandlers = append(s.onRecoverHandlers, fn)
}
func (s *Spider) handleOnError(ctx *Context, err error) {
	for _, fn := range s.onRecoverHandlers {
		fn(ctx, err)
	}
}
func (s *Spider) OnRespError(fn func(ctx *Context, err error)) {
	s.onRespErrorHandlers = append(s.onRespErrorHandlers, fn)
}
func (s *Spider) handleOnRespError(ctx *Context, err error) {
	for _, fn := range s.onRespErrorHandlers {
		fn(ctx, err)
	}
}
func (s *Spider) OnReqError(fn func(ctx *Context, err error)) {
	s.onReqErrorHandlers = append(s.onReqErrorHandlers, fn)
}
func (s *Spider) handleOnReqError(ctx *Context, err error) {
	for _, fn := range s.onReqErrorHandlers {
		fn(ctx, err)
	}
}
