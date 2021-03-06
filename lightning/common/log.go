// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package common

import (
	"bytes"
	"fmt"
	"path"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/pingcap/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/pingcap/tidb/util/logutil"
)

const (
	defaultLogTimeFormat = "2006/01/02 15:04:05.000"
	defaultLogLevel      = log.InfoLevel
	defaultLogMaxDays    = 7
	defaultLogMaxSize    = 512 // MB
)

// LogConfig serializes log related config in toml/json.
type LogConfig struct {
	// Log level.
	Level string `toml:"level" json:"level"`
	// Log filename, leave empty to disable file log.
	File string `toml:"file" json:"file"`
	// Max size for a single file, in MB.
	FileMaxSize int `toml:"max-size" json:"max-size"`
	// Max log keep days, default is never deleting.
	FileMaxDays int `toml:"max-days" json:"max-days"`
	// Maximum number of old log files to retain.
	FileMaxBackups int `toml:"max-backups" json:"max-backups"`
}

func (cfg *LogConfig) Adjust() {
	if len(cfg.File) > 0 {
		if cfg.FileMaxSize == 0 {
			cfg.FileMaxSize = defaultLogMaxSize
		}
		if cfg.FileMaxDays == 0 {
			cfg.FileMaxDays = defaultLogMaxDays
		}
	}
}

func stringToLogLevel(level string) log.Level {
	switch strings.ToLower(level) {
	case "fatal":
		return log.FatalLevel
	case "error":
		return log.ErrorLevel
	case "warn", "warning":
		return log.WarnLevel
	case "debug":
		return log.DebugLevel
	case "info":
		return log.InfoLevel
	}
	return defaultLogLevel
}

type SimpleTextFormater struct{}

func (f *SimpleTextFormater) Format(entry *log.Entry) ([]byte, error) {
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	// timestamp
	fmt.Fprintf(b, "%s ", entry.Time.Format(defaultLogTimeFormat))
	// code stack trace
	if file, ok := entry.Data["file"]; ok {
		fmt.Fprintf(b, "%s:%v:", file, entry.Data["line"])
	}
	// level + message
	fmt.Fprintf(b, " [%s] %s", entry.Level.String(), entry.Message)

	// others
	for k, v := range entry.Data {
		if k != "file" && k != "line" {
			fmt.Fprintf(b, " %v=%v", k, v)
		}
	}

	b.WriteByte('\n')

	return b.Bytes(), nil
}

// modifyHook injects file name and line pos into log entry.
type contextHook struct{}

// Levels implements logrus.Hook interface.
func (hook *contextHook) Levels() []log.Level {
	return log.AllLevels
}

// Fire implements logrus.Hook interface
// https://github.com/sirupsen/logrus/issues/63
func (hook *contextHook) Fire(entry *log.Entry) error {
	pc := make([]uintptr, 3)
	cnt := runtime.Callers(6, pc)

	for i := 0; i < cnt; i++ {
		fu := runtime.FuncForPC(pc[i] - 1)
		name := fu.Name()
		if !isSkippedPackageName(name) {
			file, line := fu.FileLine(pc[i] - 1)
			entry.Data["file"] = path.Base(file)
			entry.Data["line"] = line
			break
		}
	}
	return nil
}

func isSkippedPackageName(name string) bool {
	return strings.Contains(name, "github.com/sirupsen/logrus") ||
		strings.Contains(name, "github.com/coreos/pkg/capnslog")
}

// AppLogger is a logger for lightning, different from tidb logger.
var AppLogger = log.New()

func SetLevel(level log.Level) {
	atomic.StoreUint32((*uint32)(&AppLogger.Level), uint32(level))
}

func GetLevel() (level log.Level) {
	return log.Level(atomic.LoadUint32((*uint32)(&AppLogger.Level)))
}

func InitLogger(cfg *LogConfig, tidbLoglevel string) error {
	SetLevel(stringToLogLevel(cfg.Level))
	AppLogger.Hooks.Add(&contextHook{})
	AppLogger.Formatter = &SimpleTextFormater{}

	logutil.InitLogger(&logutil.LogConfig{Level: tidbLoglevel})

	if len(cfg.File) > 0 {
		if IsDirExists(cfg.File) {
			return errors.Errorf("can't use directory as log file name : %s", cfg.File)
		}

		// use lumberjack to logrotate
		output := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxAge:     cfg.FileMaxDays,
			MaxSize:    cfg.FileMaxSize,
			MaxBackups: cfg.FileMaxBackups,
			LocalTime:  true,
		}

		AppLogger.Out = output
	}

	return nil
}
