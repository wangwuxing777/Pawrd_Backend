# WORK_CONTEXT — Pawrd_Backend

**TASK_ID:** TASK-20260601-1820
**更新时间:** 2026-06-01 18:20
**完整记录:** [→ 根目录 master_progress.md](../../master_progress.md)

## 当前任务
收口 Go RAG 回答链路，删除 deterministic / hardcode 逻辑，只保留纯检索、可选 rerank 与 LLM summarization，并清理结构化证据污染。

## 状态速览
- ✅ 已完成：删除回答层 deterministic 模板
- ✅ 已完成：删除检索层 provider 重排与 benefit 定向加权
- ✅ 已完成：清理结构化 sub coverage remark 串行污染
- 🔄 进行中：继续提升 waiting period / compare 查询的纯检索召回质量
- ⏳ 待处理：必要时继续补 corpus / chunking / rerank，而不是回退到模板答案

## 本次涉及的关键文件
- `internal/services/raggo/runtime.go` — 纯检索 + rerank + summarization 回答链路，移除 deterministic 重排/加权
- `internal/services/raggo/corpus.go` — 结构化 DB chunk 生成与 remark 污染过滤
