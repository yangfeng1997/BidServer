package logger

import (
	"fmt"

	configgen "project/config/gen"
	opt "project/internal/core/options"
	"project/pkg/logger"
)

var (
	Main    logger.Logger
	Res     logger.Logger
	Tracing logger.Logger

	mainCloser    *logger.LogCloser
	resCloser     *logger.LogCloser
	tracingCloser *logger.LogCloser
)

type LoggerGroup struct{}

func NewLoggerGroup(opts opt.BaseOptions, loggerGroupCfg configgen.LoggerGroupConfig) (*LoggerGroup, error) {
	mainLogger, newMainCloser, err := NewMainLogger(opts, loggerGroupCfg.Main)
	if err != nil {
		return nil, err
	}
	resLogger, newResCloser, err := NewResLogger(opts, loggerGroupCfg.Res)
	if err != nil {
		_ = newMainCloser.Close()
		return nil, err
	}
	tracingLogger, newTracingCloser, err := NewTracingLogger(opts, loggerGroupCfg.Tracing)
	if err != nil {
		_ = newMainCloser.Close()
		_ = newResCloser.Close()
		return nil, err
	}

	Main = mainLogger
	Res = resLogger
	Tracing = tracingLogger
	mainCloser = newMainCloser
	resCloser = newResCloser
	tracingCloser = newTracingCloser
	logger.SetGlobal(Main)

	return &LoggerGroup{}, nil
}

func (group *LoggerGroup) Shutdown() {
	closeLogger("main", mainCloser)
	closeLogger("res", resCloser)
	closeLogger("tracing", tracingCloser)
}

func closeLogger(name string, closer *logger.LogCloser) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		logger.Error("close logger failed", logger.String("name", name), logger.Err(err))
	}
}

func NewMainLogger(opts opt.BaseOptions, cfg configgen.LogConfig) (logger.Logger, *logger.LogCloser, error) {
	return newFileLogger(opts, cfg)
}

func NewResLogger(opts opt.BaseOptions, cfg configgen.LogConfig) (logger.Logger, *logger.LogCloser, error) {
	return newFileLogger(opts, cfg)
}

func NewTracingLogger(opts opt.BaseOptions, cfg configgen.LogConfig) (logger.Logger, *logger.LogCloser, error) {
	return newFileLogger(opts, cfg)
}

func newFileLogger(opts opt.BaseOptions, cfg configgen.LogConfig) (logger.Logger, *logger.LogCloser, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	format, err := parseFormat(cfg.Format)
	if err != nil {
		return nil, nil, err
	}
	return logger.NewZapFileLogger(logger.FileLoggerConfig{
		Level:      level,
		Format:     format,
		StderrAlso: cfg.StderrAlso && !opts.Daemon,
		Rotate: logger.RotateConfig{
			Dir:          cfg.Dir,
			Basename:     cfg.Basename,
			MaxSizeMB:    int(cfg.MaxSizeMb),
			MaxBackups:   int(cfg.MaxBackups),
			RotateByHour: cfg.RotateByHour,
		},
	})
}

func parseLevel(value string) (logger.Level, error) {
	var level logger.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, fmt.Errorf("parse log level: %w", err)
	}
	return level, nil
}

func parseFormat(value string) (logger.Format, error) {
	switch value {
	case "", string(logger.FormatConsole):
		return logger.FormatConsole, nil
	case string(logger.FormatJSON):
		return logger.FormatJSON, nil
	default:
		return "", fmt.Errorf("unknown log format %q", value)
	}
}
