package logger

import (
	"time"

	baselogger "project/pkg/logger"
)

type Field = baselogger.Field

func String(key, val string) Field {
	return baselogger.String(key, val)
}

func Int(key string, val int) Field {
	return baselogger.Int(key, val)
}

func Int32(key string, val int32) Field {
	return baselogger.Int32(key, val)
}

func Int64(key string, val int64) Field {
	return baselogger.Int64(key, val)
}

func Uint32(key string, val uint32) Field {
	return baselogger.Uint32(key, val)
}

func Uint64(key string, val uint64) Field {
	return baselogger.Uint64(key, val)
}

func Float64(key string, val float64) Field {
	return baselogger.Float64(key, val)
}

func Bool(key string, val bool) Field {
	return baselogger.Bool(key, val)
}

func Duration(key string, val time.Duration) Field {
	return baselogger.Duration(key, val)
}

func Time(key string, val time.Time) Field {
	return baselogger.Time(key, val)
}

func Err(err error) Field {
	return baselogger.Err(err)
}

func NamedErr(key string, err error) Field {
	return baselogger.NamedErr(key, err)
}

func Any(key string, val any) Field {
	return baselogger.Any(key, val)
}
