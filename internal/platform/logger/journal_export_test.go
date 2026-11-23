//go:build unix

package logger

// SetJournalStdoutSocket 替换 journald stdout socket 的路径，测试结束时恢复。
func SetJournalStdoutSocket(path string) (restore func()) {
	old := journalStdoutSocket
	journalStdoutSocket = path
	return func() { journalStdoutSocket = old }
}
