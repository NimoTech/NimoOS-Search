# NimoOS-Search

NimoOS RAG retrieval API service — aggregates a filename index and vector
recall across multiple sources behind `/v1/search`.

NimoOS 的检索服务：把文件名索引与向量召回多源聚合，对外提供 `/v1/search`。
架构与运行时细节见 [`OVERVIEW.md`](./OVERVIEW.md)。

> ### About / 关于本项目
>
> NimoOS is a fork of [CasaOS](https://github.com/IceWhaleTech/CasaOS)
> (Apache-2.0), originally developed by IceWhale Technology Co., Ltd.
> Building on that foundation, NimoOS adds an AI agent, RAG-based
> retrieval, a knowledge layer, and a built-in web terminal.
>
> NimoOS 基于 [CasaOS](https://github.com/IceWhaleTech/CasaOS)（Apache-2.0）
> fork 而来，原始项目由 IceWhale Technology Co., Ltd. 开发。在此基础上，
> NimoOS 重建了 AI Agent、RAG 检索、知识库与内置终端等能力。
>
> 归属详情见 [`NOTICE`](./NOTICE)。CasaOS 与 IceWhale 是 IceWhale Technology
> Co., Ltd. 的商标；NimoOS 是独立项目，与 IceWhale 无隶属关系。
>
> 本仓库是 NimoTech 原创，不含 CasaOS 衍生代码。

> ⚠️ Multi-user isolation is incomplete — Photos and Search are not yet
> per-user scoped. Read
> [SECURITY.md](https://github.com/NimoTech/NimoOS/blob/main/SECURITY.md#known-limitations)
> before deploying NimoOS for more than one person.
>
> ⚠️ 多用户隔离尚不完整（Photos 与搜索未按用户隔离）。若要给多人使用，请先阅读
> [SECURITY.md](https://github.com/NimoTech/NimoOS/blob/main/SECURITY.md#known-limitations)。

## Build
```bash
go build -o nimoos-search .
```

## Dev
```bash
go test ./...
```
