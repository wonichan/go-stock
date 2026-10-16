# 由 Debian /usr/bin/chromium 启动脚本 source（/etc/chromium.d/*）。
# go-stock Web 镜像：容器内以非 root 运行且通常没有 user namespace，Chromium 沙箱不可用；
# 另外在部署层强制无头，确保任何抓取都不会尝试打开窗口。
export CHROMIUM_FLAGS="$CHROMIUM_FLAGS --no-sandbox --headless=new --disable-dev-shm-usage --disable-gpu"
