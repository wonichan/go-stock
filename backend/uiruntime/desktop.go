//go:build !server

package uiruntime

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ServerMode 为 false：桌面构建，全部转发给 Wails runtime。
const ServerMode = false

// SetEventSink 桌面构建下无意义（事件由 Wails 投递），保留以便 webbridge 两种构建都能编译。
func SetEventSink(EventSink) {}

// DispatchFrontendEvent 桌面构建下无意义（前端事件由 Wails 投递）。
func DispatchFrontendEvent(string, []interface{}) {}

func EventsEmit(ctx context.Context, eventName string, optionalData ...interface{}) {
	wruntime.EventsEmit(ctx, eventName, optionalData...)
}

func EventsOn(ctx context.Context, eventName string, callback func(optionalData ...interface{})) func() {
	return wruntime.EventsOn(ctx, eventName, callback)
}

func EventsOff(ctx context.Context, eventName string, additionalEventNames ...string) {
	wruntime.EventsOff(ctx, eventName, additionalEventNames...)
}

func OpenFileDialog(ctx context.Context, dialogOptions OpenDialogOptions) (string, error) {
	return wruntime.OpenFileDialog(ctx, dialogOptions)
}

func OpenMultipleFilesDialog(ctx context.Context, dialogOptions OpenDialogOptions) ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(ctx, dialogOptions)
}

func SaveFileDialog(ctx context.Context, dialogOptions SaveDialogOptions) (string, error) {
	return wruntime.SaveFileDialog(ctx, dialogOptions)
}

func MessageDialog(ctx context.Context, dialogOptions MessageDialogOptions) (string, error) {
	return wruntime.MessageDialog(ctx, dialogOptions)
}

func BrowserOpenURL(ctx context.Context, url string) { wruntime.BrowserOpenURL(ctx, url) }

func Quit(ctx context.Context) { wruntime.Quit(ctx) }

func ScreenGetAll(ctx context.Context) ([]Screen, error) { return wruntime.ScreenGetAll(ctx) }

func WindowSetBackgroundColour(ctx context.Context, R, G, B, A uint8) {
	wruntime.WindowSetBackgroundColour(ctx, R, G, B, A)
}
func WindowSetDarkTheme(ctx context.Context)  { wruntime.WindowSetDarkTheme(ctx) }
func WindowSetLightTheme(ctx context.Context) { wruntime.WindowSetLightTheme(ctx) }
func WindowReloadApp(ctx context.Context)     { wruntime.WindowReloadApp(ctx) }
func WindowShow(ctx context.Context)          { wruntime.WindowShow(ctx) }
func WindowHide(ctx context.Context)          { wruntime.WindowHide(ctx) }
func WindowUnminimise(ctx context.Context)    { wruntime.WindowUnminimise(ctx) }
func WindowFullscreen(ctx context.Context)    { wruntime.WindowFullscreen(ctx) }
func WindowUnfullscreen(ctx context.Context)  { wruntime.WindowUnfullscreen(ctx) }
func WindowCenter(ctx context.Context)        { wruntime.WindowCenter(ctx) }
func WindowSetSize(ctx context.Context, width, height int) {
	wruntime.WindowSetSize(ctx, width, height)
}
func WindowGetSize(ctx context.Context) (int, int) { return wruntime.WindowGetSize(ctx) }
