package queue

import "github.com/riverqueue/river"

// Periodic 描述一个定时任务，由模块的 PeriodicJobs() 提供。不直接用 river.PeriodicJob：
// 它的字段不导出，组合根的测试读不到会入队什么 kind，也就查不出"定时任务没有 worker"。
type Periodic struct {
	// Schedule 如 river.PeriodicInterval(time.Hour)。
	Schedule river.PeriodicSchedule
	// Args 每次入队都相同；截止时间之类在 Work 里按当前时间计算。
	Args river.JobArgs
	Opts *river.PeriodicJobOpts
}

func (p Periodic) riverJob() *river.PeriodicJob {
	return river.NewPeriodicJob(p.Schedule, func() (river.JobArgs, *river.InsertOpts) {
		return p.Args, nil
	}, p.Opts)
}
