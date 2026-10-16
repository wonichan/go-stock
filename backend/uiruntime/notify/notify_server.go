//go:build server

package notify

import "go-stock/backend/logger"

// Notify 在 Web 服务模式下为 no-op（仅记录 debug 日志）。
func Notify(title, message string, _ any) error {
	logger.SugaredLogger.Debugf("notify (server mode, suppressed): %s %s", title, message)
	return nil
}
