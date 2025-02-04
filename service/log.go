package service

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	logIndex   = 0
	logRWMutex = new(sync.RWMutex)
	logCache   [10000]string
)

func DebugF(format string, args ...interface{}) {
	if !Debug {
		return
	}
	format = fmt.Sprintf("[%s] %s", time.Now().In(time.Local).Format(time.DateTime), format)
	logRWMutex.Lock()
	defer logRWMutex.Unlock()
	if logIndex == len(logCache) {
		logIndex = 0
	}
	logCache[logIndex] = fmt.Sprintf(format, args...)
	logIndex++
}

func WriteLog(w io.Writer) error {
	logRWMutex.RLock()
	defer logRWMutex.RUnlock()
	for i := 0; i < len(logCache); i++ {
		if logCache[i] == "" {
			break
		}
		fmt.Fprintln(w, logCache[i])
	}
	return nil
}
