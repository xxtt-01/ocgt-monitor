## 2026-06-11: 配置 Gitee 远程仓库
- **文件:** `.git/config`
- **原因:** 用户要求将项目同步到 Gitee
- **决策:** origin 指向 Gitee（触发 post-commit 自动推送），GitHub 保留为 github remote
- **影响范围:** git 远程配置变更

## 2026-06-11: 发布 v0.5.0 到 GitHub Releases
- **文件:** `ocgt-monitor.exe`（重新编译）
- **原因:** 用户要求上传最新 exe 到 GitHub 方便下载
- **影响范围:** GitHub Releases v0.5.0（[044]-[049] 的改动）
