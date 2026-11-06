package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var logState struct {
	mu   sync.Mutex
	f    *os.File
	ring []string
	path string
}

func initLog(dir string) {
	logState.mu.Lock()
	defer logState.mu.Unlock()
	p := filepath.Join(dir, "mishk.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		logState.f = f
		logState.path = p
	}
}

func logf(format string, a ...any) {
	line := time.Now().Format("15:04:05.000 ") + fmt.Sprintf(format, a...)
	logState.mu.Lock()
	defer logState.mu.Unlock()
	logState.ring = append(logState.ring, line)
	if len(logState.ring) > 400 {
		logState.ring = logState.ring[len(logState.ring)-400:]
	}
	if logState.f != nil {
		_, _ = logState.f.WriteString(line + "\n")
	}
}

func logText() string {
	logState.mu.Lock()
	defer logState.mu.Unlock()
	return strings.Join(logState.ring, "\n")
}

func hexs(b []byte) string {
	var sb strings.Builder
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", x)
	}
	return sb.String()
}
