## 改了什么

<!-- 一两句说明问题与方案；关联 issue 用 #123 -->

## 验证

- [ ] `apps/api`: `gofmt -l .` / `go vet ./...` / `go test ./...`
- [ ] `apps/web`: `npm ci && npm run build`（必须带生产 VITE_*，否则 Rolldown 会裁掉入口）
- [ ] 涉及迁移/配置/部署时，已记录回滚方式

## 风险与回滚

<!-- 影响面、灰度方式、回滚步骤；没有就写"无" -->
