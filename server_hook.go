package main

// runServer 由 main_server.go（-tags server）在 init 中设置；桌面构建下为 nil，
// main() 照常走 wails.Run。
var runServer func(app *App)
