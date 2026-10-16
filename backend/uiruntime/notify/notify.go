// Package notify 封装系统桌面通知（beeep）。
// 业务代码以 `beeep "go-stock/backend/uiruntime/notify"` 导入，调用写法与 beeep 一致。
// server 构建下没有桌面会话，通知降级为 debug 日志（需要推送请使用钉钉/飞书等渠道）。
package notify
