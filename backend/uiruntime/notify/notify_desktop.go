//go:build !server

package notify

import "github.com/gen2brain/beeep"

// Notify 转发给 beeep.Notify。
func Notify(title, message string, appIcon any) error {
	return beeep.Notify(title, message, appIcon)
}
