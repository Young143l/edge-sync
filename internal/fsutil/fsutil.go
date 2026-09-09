// fsutil 掉电安全的原子写：write → fsync → rename。
// 备份工具对掉电容忍要求高：rename 后若数据未真正落盘，
// 可能留下「文件名已换好、内容为空/半截」的文件。
package fsutil

import (
	"os"
)

// WriteFileAtomic 原子写文件：临时文件写入 + fsync + rename。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
