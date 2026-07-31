# Pawrd Backend

## 启动

支持 macOS。全新电脑只需在项目根目录运行：

```bash
chmod +x start.sh
./start.sh
```

脚本会自动安装 Homebrew（如缺失）和 Go、下载 Go 模块、首次生成安全的本地 `.env`，然后启动服务：

- API：`http://localhost:8000`
- RAG 测试页：`http://localhost:8000/rag-test`

按 `Ctrl + C` 停止服务。再次运行同一命令会复用现有 `.env`，不会覆盖你的配置。

默认配置使用本地 SQLite，并关闭真实支付、Shopify、HiCustom 和外部密钥能力，因此不需要 PostgreSQL 或第三方账号即可完成基础启动。
