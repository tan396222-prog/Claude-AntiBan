// Package usererror describes known system failures without exposing raw
// command arguments, network responses or credentials in the user interface.
package usererror

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unicode"
)

type failure struct {
	message string
	cause   error
}

func (e *failure) Error() string { return e.message }
func (e *failure) Unwrap() error { return e.cause }

func New(action, reason, next string, cause error) error {
	return &failure{message: action + "失败：" + strings.TrimRight(reason, "。；;") + "。" + next, cause: cause}
}

func Wrap(action string, cause error, next string) error {
	if cause == nil {
		return nil
	}
	reason := Reason(cause)
	if reason == "" {
		reason = "未能取得可识别的错误原因"
	}
	return New(action, reason, next, cause)
}

// Existing Chinese validation messages describe product rules and must survive
// unchanged. Unknown system exceptions receive an operation and a next step.
func Display(action string, cause error, next string) error {
	if cause == nil {
		return nil
	}
	switch cause.(type) {
	case *fs.PathError, *os.SyscallError, syscall.Errno, *exec.ExitError, net.Error:
		return Wrap(action, cause, next)
	}
	if strings.IndexFunc(cause.Error(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		return cause
	}
	return Wrap(action, cause, next)
}

func Reason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "操作已取消"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "等待响应超时"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "无法解析服务器域名"
	}
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &certificate) {
		return "服务器证书验证未通过"
	}
	if errors.As(err, &record) {
		return "服务器没有返回有效的加密连接响应"
	}
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "系统拒绝访问所需文件或目录"
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, exec.ErrNotFound):
		return "所需文件或程序不存在"
	case errors.Is(err, syscall.ENOSPC) || runtime.GOOS == "windows" && (errors.Is(err, syscall.Errno(112)) || errors.Is(err, syscall.Errno(39))):
		return "磁盘剩余空间不足"
	case errors.Is(err, syscall.EROFS):
		return "目标磁盘为只读，无法写入"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "目标服务拒绝了连接"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "连接在返回完整结果前中断"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "当前网络无法到达目标服务"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("程序提前结束（退出码 %d），未返回可识别的原因", exit.ExitCode())
	}
	return ""
}
