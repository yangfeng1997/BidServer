package pidfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Write 将 pid 写入指定路径的 pidfile，权限 0644。
func Write(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0644)
}

// Read 读取 pidfile 并返回其中的 PID。
func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// TryWrite 以独占方式创建 pid 文件并写入 pid。
// 若文件已存在（另一进程已写入），返回 os.ErrExist；
// 调用方可据此区分"已有进程"和其他 IO 错误。
func TryWrite(path string, pid int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d\n", pid)
	return err
}

// Remove 删除 pidfile；若文件不存在则忽略。
func Remove(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
