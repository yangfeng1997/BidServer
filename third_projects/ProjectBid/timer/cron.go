package timer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronSchedule 基于标准 5 字段 cron 表达式（分钟 小时 日期 月份 星期）的调度器。
// 不支持特殊字符 L、W、#、?（仅支持 */数字 和 - 范围）。
type CronSchedule struct {
	minutes  fieldMatcher
	hours    fieldMatcher
	days     fieldMatcher
	months   fieldMatcher
	weekdays fieldMatcher
}

// NewCronSchedule 解析 cron 表达式并创建调度器。
// 格式: "min hour day month weekday"（空格分隔），例 "0 4 * * *" 表示每天凌晨 4 点。
func NewCronSchedule(expr string) (*CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式需 5 个字段，实际 %d: %q", len(fields), expr)
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("分钟字段 %q: %w", fields[0], err)
	}
	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("小时字段 %q: %w", fields[1], err)
	}
	days, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("日期字段 %q: %w", fields[2], err)
	}
	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("月份字段 %q: %w", fields[3], err)
	}
	weekdays, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("星期字段 %q: %w", fields[4], err)
	}

	return &CronSchedule{
		minutes:  minutes,
		hours:    hours,
		days:     days,
		months:   months,
		weekdays: weekdays,
	}, nil
}

// Next 返回 prev 之后的下一次触发时间。
func (c *CronSchedule) Next(prev time.Time) time.Time {
	// 从 prev 的下一个分钟开始搜索
	next := prev.Add(1 * time.Minute)
	next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), 0, 0, next.Location())

	// 最多搜索 366 天（覆盖闰年）
	for i := 0; i < 525600; i++ {
		if !c.months.match(int(next.Month())) {
			next = time.Date(next.Year(), next.Month()+1, 1, 0, 0, 0, 0, next.Location())
			continue
		}
		if !c.days.match(next.Day()) && !c.weekdays.match(int(next.Weekday())) {
			next = next.Add(24 * time.Hour)
			next = time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location())
			continue
		}
		if !c.hours.match(next.Hour()) {
			next = next.Add(1 * time.Hour)
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), 0, 0, 0, next.Location())
			continue
		}
		if !c.minutes.match(next.Minute()) {
			next = next.Add(1 * time.Minute)
			continue
		}
		return next
	}
	// 一年内无匹配，返回零值
	return time.Time{}
}

// ——— fieldMatcher ———

type fieldMatcher interface {
	match(v int) bool
}

// starMatcher 匹配所有值（*）。
type starMatcher struct{}

func (m *starMatcher) match(v int) bool { return true }

// exactMatcher 匹配单个精确值。
type exactMatcher struct {
	values []int
}

func (m *exactMatcher) match(v int) bool {
	for _, val := range m.values {
		if val == v {
			return true
		}
	}
	return false
}

// stepMatcher 匹配步进值（*/N）。
type stepMatcher struct {
	step int
}

func (m *stepMatcher) match(v int) bool { return v%m.step == 0 }

// rangeMatcher 匹配范围值（A-B）。
type rangeMatcher struct {
	lo, hi int
}

func (m *rangeMatcher) match(v int) bool { return v >= m.lo && v <= m.hi }

// parseField 解析单个 cron 字段。
func parseField(field string, min, max int) (fieldMatcher, error) {
	if field == "*" {
		return &starMatcher{}, nil
	}

	// */N 步进
	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil || step < 1 || step > max {
			return nil, fmt.Errorf("无效步进值: %s", field)
		}
		return &stepMatcher{step: step}, nil
	}

	// A-B 范围
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		lo, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("无效范围起始: %s", parts[0])
		}
		hi, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("无效范围结束: %s", parts[1])
		}
		if lo < min || lo > max || hi < min || hi > max || lo > hi {
			return nil, fmt.Errorf("范围超出 [%d,%d]: %d-%d", min, max, lo, hi)
		}
		return &rangeMatcher{lo: lo, hi: hi}, nil
	}

	// 逗号分隔的离散值
	parts := strings.Split(field, ",")
	if len(parts) > 1 {
		var vals []int
		for _, p := range parts {
			v, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || v < min || v > max {
				return nil, fmt.Errorf("无效值: %s", p)
			}
			vals = append(vals, v)
		}
		return &exactMatcher{values: vals}, nil
	}

	// 单个值
	v, err := strconv.Atoi(field)
	if err != nil || v < min || v > max {
		return nil, fmt.Errorf("无效值 %s，范围 [%d,%d]", field, min, max)
	}
	return &exactMatcher{values: []int{v}}, nil
}
