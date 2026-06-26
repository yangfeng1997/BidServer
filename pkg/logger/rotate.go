package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RotateConfig 滚动日志配置
type RotateConfig struct {
	Dir          string `yaml:"dir"`
	Basename     string `yaml:"basename"`
	MaxSizeMB    int    `yaml:"max_size_mb"`
	MaxBackups   int    `yaml:"max_backups"`
	RotateByHour bool   `yaml:"rotate_by_hour"`
}

// rotatingWriter 实现按小时 + 文件大小双维度滚动的 io.WriteCloser
//
// 文件命名规则（RotateByHour=true）
//
// 当前写入：scenesvr.20260528-02.log         ← 最新
// 超大小后：scenesvr.20260528-02.1.log        ← 次新
// scenesvr.20260528-02.2.log        ← 更旧
// 换小时后：scenesvr.20260528-03.log          ← 新小时的当前文件
// 软链接：  scenesvr.log → 当前写入文件
type rotatingWriter struct {
	cfg     RotateConfig
	mu      sync.Mutex
	file    *os.File
	written int64
	hour    string // 当前文件所属小时，格式 "20060102-15"
}

// NewRotatingWriter 创建滚动写入器
func NewRotatingWriter(cfg RotateConfig) (io.WriteCloser, error) {
	if cfg.Dir == "" {
		cfg.Dir = "."
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	w := &rotatingWriter{cfg: cfg}
	if err := w.openLocked(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.needRotate(now, int64(len(p))) {
		if err := w.rotateLocked(now); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// // Sync 实现 zapcore.WriteSyncer
func (w *rotatingWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// Rotate 手动触发一次滚动，供外部调用（如信号触发）
func (w *rotatingWriter) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked(time.Now())
}

func (w *rotatingWriter) needRotate(now time.Time, writeLen int64) bool {
	if w.cfg.RotateByHour && w.currentHour(now) != w.hour {
		return true
	}
	if w.cfg.MaxSizeMB > 0 && w.written+writeLen > int64(w.cfg.MaxSizeMB)*1024*1024 {
		return true
	}
	return false
}

func (w *rotatingWriter) rotateLocked(now time.Time) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil

		newHour := w.currentHour(now)
		sameHour := w.cfg.RotateByHour && newHour == w.hour
		if sameHour || !w.cfg.RotateByHour {
			// // 同小时内超大小：把现有 .1 挤成 .2，当前文件变 .1
			// 当前文件变成 .1，再新建当前文件继续写
			w.shiftAndArchiveLocked()
		}
		// 跨小时滚动：当前文件名已带时间戳，直接新建新小时文件即可
	}
	return w.openLocked(now)
}

// // 将 .1→.2, .2→.3，当前无后缀文件归档为 .1
func (w *rotatingWriter) shiftAndArchiveLocked() {
	// 找出该小时最大 seq
	maxSeq := 0
	for {
		if _, err := os.Stat(w.seqFilename(w.hour, maxSeq+1)); os.IsNotExist(err) {
			break
		}
		maxSeq++
		if maxSeq > 9999 {
			break
		}
	}

	// 从高到低依次 rename，避免覆盖
	for i := maxSeq; i >= 1; i-- {
		_ = os.Rename(w.seqFilename(w.hour, i), w.seqFilename(w.hour, i+1))
	}

	// // 当前文件 → .1
	_ = os.Rename(w.currentFilename(w.hour), w.seqFilename(w.hour, 1))
}

func (w *rotatingWriter) openLocked(now time.Time) error {
	h := w.currentHour(now)
	name := w.currentFilename(h)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.written = info.Size()
	w.hour = h

	if w.cfg.RotateByHour {
		w.updateSymlinkLocked(name)
	}
	w.cleanOldFilesLocked()
	return nil
}

// // 更新软链接 → 当前写入文件
// 优先创建 symlink；Windows 无权限时降级为 hardlink（同卷，无需特殊权限）
func (w *rotatingWriter) updateSymlinkLocked(target string) {
	link := filepath.Join(w.cfg.Dir, w.cfg.Basename+".log")
	rel, err := filepath.Rel(w.cfg.Dir, target)
	if err != nil {
		rel = target
	}
	_ = os.Remove(link)
	if os.Symlink(rel, link) != nil {
		_ = os.Link(target, link)
	}
}

// currentFilename 当前写入文件（无 seq 后缀）
func (w *rotatingWriter) currentFilename(hour string) string {
	if w.cfg.RotateByHour {
		return filepath.Join(w.cfg.Dir, fmt.Sprintf("%s.%s.log", w.cfg.Basename, hour))
	}
	return filepath.Join(w.cfg.Dir, fmt.Sprintf("%s.log", w.cfg.Basename))
}

// seqFilename 归档文件（有 seq 后缀，seq 越小越新）
func (w *rotatingWriter) seqFilename(hour string, seq int) string {
	if w.cfg.RotateByHour {
		return filepath.Join(w.cfg.Dir, fmt.Sprintf("%s.%s.%d.log", w.cfg.Basename, hour, seq))
	}
	return filepath.Join(w.cfg.Dir, fmt.Sprintf("%s.%d.log", w.cfg.Basename, seq))
}

func (w *rotatingWriter) currentHour(t time.Time) string {
	return t.Format("20060102-15")
}

type fileEntry struct {
	path  string
	mtime time.Time
}

// cleanOldFilesLocked 按 mtime 降序保留最新的 MaxBackups 个历史文件，删除多余的
// 用 mtime 而非文件名排序，是因为同小时内 seq 小的反而更新，文件名字典序不能正确反映新旧
func (w *rotatingWriter) cleanOldFilesLocked() {
	if w.cfg.MaxBackups <= 0 {
		return
	}
	prefix := w.cfg.Basename + "."
	symlinkName := w.cfg.Basename + ".log"
	currentBase := filepath.Base(w.currentFilename(w.hour))

	entries, err := os.ReadDir(w.cfg.Dir)
	if err != nil {
		return
	}

	var candidates []fileEntry
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == symlinkName || n == currentBase {
			continue
		}
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, fileEntry{
			path:  filepath.Join(w.cfg.Dir, n),
			mtime: info.ModTime(),
		})
	}

	// mtime 降序：index 0 最新，末尾最旧
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})

	if len(candidates) > w.cfg.MaxBackups {
		for _, f := range candidates[w.cfg.MaxBackups:] {
			_ = os.Remove(f.path)
		}
	}
}
