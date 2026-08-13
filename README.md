# G3M Pro 电量托盘

一个使用 Go 编写的 Windows 托盘程序，用于显示漫步者 G3M Pro 鼠标当前的电量、连接方式和充电状态，不需要启动 HECATE Connect。

## 功能

- 支持 USB 有线连接和 2.4G 接收器连接；
- 托盘图标随电量变化，并在充电时显示充电标识；
- Tooltip 和右键菜单显示电量、连接方式和文字化状态；
- 默认每 5 秒查询一次设备状态。

## 工作原理

G3M Pro 会暴露一个厂商自定义 HID 集合。程序枚举 HID 设备，定位以下特征的集合：

```text
VID         0x320F
Usage Page  0xFF1C
Usage       0x0092
```

然后通过 Windows HID 的 `WriteFile` 发送 64 字节查询报文：

```text
04 20 00 1A 06 00 00 00 00 ...
```

设备返回的响应报文中：

- 第 8 字节是电量百分比；
- 第 9 字节是设备状态，用于判断普通状态、已充满或正在充电。

程序直接访问 HID 设备，不依赖 HECATE Connect，也不需要安装专用驱动。当前实测有线模式对应 `PID_706B`，2.4G 接收器对应 `PID_706E`。

## 构建

在 Windows 上执行：

```text
go build -ldflags="-H=windowsgui" -trimpath -o g3m-battery.exe .
```

生成的 `g3m-battery.exe` 可以直接运行。程序不需要管理员权限。
