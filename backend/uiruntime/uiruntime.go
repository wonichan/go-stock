// Package uiruntime 是 Wails runtime 的薄封装层。
//
// 业务代码统一以 `runtime "go-stock/backend/uiruntime"` 导入本包（与原先导入
// github.com/wailsapp/wails/v2/pkg/runtime 的调用写法完全一致），从而：
//   - 桌面构建（默认，!server）：所有函数原样转发给 Wails runtime，行为与上游一致；
//   - Web 服务构建（-tags server）：不存在 Wails 窗口上下文，直接调用 Wails runtime 会
//     log.Fatalf 结束进程，因此改为转发给 Web 事件总线（EventsEmit → WebSocket 广播）
//     或降级为 no-op / 服务端实现（保存对话框 → 导出目录 + 浏览器下载）。
//
// 新增调用的 Wails runtime 函数时，需在 desktop.go 与 server.go 中同时补充同名函数。
package uiruntime

import (
	"go-stock/backend/apppath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 类型与常量直接复用 Wails 定义（type alias），两种构建下保持完全一致。
type (
	FileFilter           = wruntime.FileFilter
	OpenDialogOptions    = wruntime.OpenDialogOptions
	SaveDialogOptions    = wruntime.SaveDialogOptions
	MessageDialogOptions = wruntime.MessageDialogOptions
	DialogType           = wruntime.DialogType
	Screen               = wruntime.Screen
)

const (
	InfoDialog     = wruntime.InfoDialog
	WarningDialog  = wruntime.WarningDialog
	ErrorDialog    = wruntime.ErrorDialog
	QuestionDialog = wruntime.QuestionDialog
)

// EventSink 接收后端发往前端的事件（仅 server 构建使用）。
type EventSink func(eventName string, data []interface{})

// IsServerMode 报告当前二进制是否为 Web 服务构建（-tags server）。
func IsServerMode() bool { return ServerMode }

// ExportsDir 是 server 构建下「保存文件对话框」的落盘目录（<数据根>/exports），
// 文件写完后通过 /download/<name> 提供给浏览器下载。
func ExportsDir() string { return apppath.File("exports") }
