# G3M Battery 部署与发布

本文说明 G3M Battery 的构建目标、版本号约束和 GitHub Actions 发布流程。程序当前只发布 Windows amd64 和 arm64 可执行文件，不需要安装器、管理员权限或 HECATE Connect。

## 本地构建

使用 Go `1.25` 或兼容版本，在 Windows 上执行：

```text
go build -trimpath -ldflags="-H=windowsgui" -o g3m-battery.exe ./cmd/g3m-battery
```

发布构建使用 `CGO_ENABLED=0`，并分别设置 `GOARCH=amd64` 和 `GOARCH=arm64`。

## 发布步骤

1. 将 `cmd/g3m-battery/VERSION` 改为目标版本，例如 `1.5.0`；
2. 使用 Conventional Commits 提交版本变更；
3. 创建带注释的 `v` 前缀标签，且标签版本必须与 `VERSION` 完全一致；
4. 推送 `main` 和标签，例如：

```text
git tag -a v1.5.0 -m "Release v1.5.0"
git push origin main v1.5.0
```

推送匹配 `v*.*.*` 的标签后，GitHub Actions 会构建以下文件并创建 GitHub Release：

```text
g3m-battery-windows-amd64.exe
g3m-battery-windows-arm64.exe
```

持续集成工作流会检查 Go 格式、差异空白、单元测试、`go vet` 和 Windows 目标架构构建；发布工作流另外检查 tag 与 `VERSION` 的一致性。程序内版本号来自 `cmd/g3m-battery/VERSION`，不会自动从 Git tag 生成。
