//go:build !unix

package logger

import "io"

func toJournal(io.Writer) bool { return false }
