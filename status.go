package gospider

import (
	"sync/atomic"
	"time"
)

// SpiderStatus 爬虫状态
type SpiderStatus struct { //  TODO
	TotalTask    int64 // task总数
	FinishedTask int64 // 已完成的任务数
	TotalItem    int64 // Item的总数
	ExecSpeed    int64 // 执行数据
	itemSpeed    int64
}

// NewSpiderStatus 爬虫状态初始化函数
func NewSpiderStatus() *SpiderStatus {
	s := &SpiderStatus{}
	lastFinish := int64(0)
	lastItem := int64(0)
	go func() {
		for true {
			s.ExecSpeed = (s.FinishedTask - lastFinish) / 5
			s.itemSpeed = (s.TotalItem - lastItem) / 5
			lastFinish = s.FinishedTask
			lastItem = s.TotalItem
			time.Sleep(5 * time.Second)
		}
	}()
	return s
}

// AddTask 增加task， 并记录在内存中
func (s *SpiderStatus) AddTask() {
	atomic.AddInt64(&s.TotalTask, 1)
}

// AddItem 新增 Item
func (s *SpiderStatus) AddItem() {
	atomic.AddInt64(&s.TotalTask, 1)
}

// FinishTask 新增完成任务
func (s *SpiderStatus) FinishTask() {
	atomic.AddInt64(&s.FinishedTask, 1)
}

// PrintSignalLine 打印爬虫
func (s *SpiderStatus) PrintSignalLine(name string) {
	log.Info().
		Str("spider", name).
		Int64("items/sec", s.TotalItem).
		Int64("task finished/sec", s.itemSpeed).Send()
}
