package logger

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	opt "project/internal/core/options"
	"project/pkg/logger"
)

var (
	Main    logger.Logger
	Res     logger.Logger
	Tracing logger.Logger
)

type LoggerModule struct {
	app.BaseModule
	closers []*logger.LogCloser
}

func NewLoggerModule(opts opt.BaseOptions, logGroupCfg configgen.LogGroupConfig) (*LoggerModule, error) {
	mainLogger, mainCloser, err := NewMainLogger(opts, logGroupCfg.Main)
	if err != nil {
		return nil, err
	}
	resLogger, resCloser, err := NewResLogger(opts, logGroupCfg.Res)
	if err != nil {
		_ = mainCloser.Close()
		return nil, err
	}
	tracingLogger, tracingCloser, err := NewTracingLogger(opts, logGroupCfg.Tracing)
	if err != nil {
		_ = mainCloser.Close()
		_ = resCloser.Close()
		return nil, err
	}

	Main = mainLogger
	Res = resLogger
	Tracing = tracingLogger
	logger.SetGlobal(Main)

	return &LoggerModule{closers: []*logger.LogCloser{mainCloser, resCloser, tracingCloser}}, nil
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

func (module *LoggerModule) Stop() {
	for _, closer := range module.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			logger.Error("close logger failed", logger.Err(err))
		}
	}
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
