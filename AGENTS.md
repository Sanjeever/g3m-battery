# G3M Battery 项目约定

## 项目概览

这是一个使用 Go 编写的 Windows 托盘程序，用于读取漫步者 G3M Pro 鼠标或 2.4G 接收器的 HID 电量状态。项目不依赖 HECATE Connect，不需要管理员权限，当前仅支持 Windows。

核心数据流是：

```text
HID 枚举与读取 → BatteryState → 历史记录/续航计算 → 托盘展示
```

## 代码结构

- `cmd/g3m-battery/hid_windows.go`：枚举 HID、筛选目标设备、发送查询报文并读取响应；
- `cmd/g3m-battery/battery_state_windows.go`：连接方式、充电状态和错误类型；
- `cmd/g3m-battery/history_windows.go`：历史采样、异常事件、续航计算和本地报告；
- `cmd/g3m-battery/main_windows.go`：监控循环、状态发布和程序生命周期；
- `cmd/g3m-battery/tray_windows.go`：隐藏窗口、托盘菜单、设备通知；
- `cmd/g3m-battery/icon_windows.go`：动态托盘图标；
- `cmd/g3m-battery/startup_windows.go`：当前用户开机启动；
- `cmd/g3m-battery/VERSION`：嵌入到程序中的版本号。

## 开发约定

- 保持 Windows 构建约束；新增 Windows API 代码使用 `//go:build windows`；
- 使用 Go `1.25` 或兼容版本，第三方依赖保持最少；
- HID 协议字段和状态映射必须保持显式，遇到未确认的原始值显示未知，不要静默猜测；
- 状态错误使用 `ErrorKind` 分类，不要在业务逻辑中解析中文错误文本；
- 历史数据保存在 `%LOCALAPPDATA%\G3M Battery\history.json`，历史详情通过应用内窗口展示；
- 修改历史数据结构时同步更新 `historyDataVersion` 或提供迁移逻辑；
- 续航估算必须明确标记为估算，数据不足时返回“暂无估算”；
- 修改 Go 文件后运行 `gofmt`，并检查 `git diff --check`；
- 保持改动小而集中，不要顺手重构无关代码或修改无关格式。

## 构建和发布

本地构建：

```text
go build -trimpath -ldflags="-H=windowsgui" -o g3m-battery.exe ./cmd/g3m-battery
```

发布流程：

1. 将 `cmd/g3m-battery/VERSION` 改为目标版本；
2. 使用 Conventional Commits 提交，例如 `chore: bump version to 1.4.0`；
3. 创建带注释的版本标签，例如 `git tag -a v1.4.0 -m "Release v1.4.0"`；
4. 推送 `main` 和对应标签，例如 `git push origin main v1.4.0`。

GitHub Actions 会在推送匹配 `v*.*.*` 的标签后构建 Windows amd64 和 arm64 产物并创建 Release。

## 验收重点

- 有线和 2.4G 接收器连接时，电量、连接方式和充电状态显示一致；
- 设备断连、读取失败和重新连接不会产生重复历史事件；
- 历史窗口能打开，24 小时和 7 天曲线能正常显示；
- 没有足够连续放电数据时，右键菜单显示“暂无估算”；
- 不要把未运行程序期间的时间跨越计算为充电或放电时长。
