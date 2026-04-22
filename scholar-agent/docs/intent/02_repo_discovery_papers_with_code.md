# Intent 优化 02：将 `repo_discovery` 改为真实 Papers with Code 查询节点

## 背景

在论文复现链路里，原来的 `repo_discovery` 节点虽然名字叫“定位参考仓库”，但实际执行方式更接近：

- planner 先生成一个 `repo_discovery` 节点
- 调度器把它交给 `coder_agent`
- `coder_agent` 再走通用 LLM 文本生成链路

这会带来两个问题：

- `repo_url` 可能只是模型生成的一段文本，不一定是真实可访问仓库
- 无法给出稳定、可复现、可解释的“论文 -> 仓库”定位依据

因此，这次改造的目标是把 `repo_discovery` 从“描述型 LLM 节点”升级为“真实联网查询节点”。

## 本次改动目标

- planner 在论文复现场景中，明确生成一个固定流程的 `repo_discovery` 节点
- `repo_discovery` 执行时不再走通用 LLM，而是由后端直接联网查询
- 输出结构化产物，而不是只返回一段自然语言：
  - `candidate_repositories`
  - `repo_validation_report`
  - `repo_url`

## 为什么叫 Papers with Code 查询

理论目标是优先使用 `Papers with Code` 做“论文 -> 代码仓库”的第一跳，因为这个平台天然适合做论文与开源实现的映射。

但在当前网络环境里，`paperswithcode.com` 的 paper 页面和相关接口路径会发生跳转，实际落到 HuggingFace 的 papers 页面。

因此当前执行层采用的是：

- 查询入口：HuggingFace Papers API
- 数据语义：承载自 Papers with Code / papers 页面体系

也就是说：

- **规划语义上**仍然是 “Papers with Code 查询节点”
- **执行实现上**当前使用的是 HuggingFace Papers API 作为真实联网入口

## planner 层改动

### 1. 模板 planner

在论文复现模板中，将仓库定位节点固定为专用节点：

- 节点名：`Retrieve Paper Repositories`
- 节点类型：`repo_discovery`
- 依赖：`parsed_paper`
- 输出：
  - `candidate_repositories`
  - `repo_validation_report`
  - `repo_url`

对应代码位置：

- [planner.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

关键函数：

- `newRepoDiscoveryNode()`
- `buildRepoDiscoveryDescription()`

### 2. LLM planner

为了让 LLM planner 在需要时也能把该节点正确组装进 plan graph，本次增加了约束：

- 论文复现需要开源实现时，必须包含 `repo_discovery`
- `repo_discovery` 必须：
  - 依赖 `parsed_paper`
  - 输出 `candidate_repositories`、`repo_validation_report`、`repo_url`
- `repo_prepare` 需要消费 `repo_url`

对应代码位置：

- [agent_planner.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/planner/agent_planner.go)

### 3. 节点契约校验

新增了论文复现链路的强约束，确保计划图里不会漏掉关键节点：

- `paper_parse`
- `repo_discovery`
- `repo_prepare`
- `resolve_dependencies`
- `prepare_runtime`
- `install_dependencies`
- `execute_code`

同时校验 `repo_discovery` 的关键 artifacts 是否完整。

对应代码位置：

- [planner.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

## 执行层改动

### 1. 改造前

原先 `repo_discovery` 节点虽然在 DAG 中存在，但执行时本质上还是交给 `coder_agent` 做 LLM 生成。

### 2. 改造后

现在 `repo_discovery` 改为调度器内置真实执行逻辑：

- 不再走 `coder_agent -> CodeOnlyChain`
- 直接在调度器里做 HTTP 查询

对应代码位置：

- [executor.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/scheduler/executor.go)
- [repo_discovery.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/scheduler/repo_discovery.go)

执行入口：

- `executeRepoDiscovery(ctx, runtimeTask)`

## 当前真实查询流程

### Step 1. 构造查询词

优先级如下：

1. 从 `parsed_paper` 中提取 arXiv ID
2. 从 `parsed_paper` 中提取论文标题
3. 从任务描述中提取 arXiv ID
4. 从任务描述中提取论文标题
5. 都没有时，退回到任务描述前 200 字

相关函数：

- `buildRepoDiscoveryQuery()`
- `extractTitleHeuristic()`

### Step 2. 查询论文

调用：

```text
GET /api/papers/search?q=...
```

当前默认基址：

```text
https://huggingface.co
```

对应函数：

- `hfPaperSearch()`

### Step 3. 读取论文详情并抽取 GitHub 仓库

对命中的论文逐个请求：

```text
GET /api/papers/{paper_id}
```

然后从返回 JSON 中提取 GitHub URL。

这里没有强绑定某个固定字段名，而是直接在原始 JSON 中匹配 GitHub 链接，目的是适配接口字段变动。

对应函数：

- `hfPaperRepos()`

### Step 4. 候选排序

当前排序是轻量规则排序：

- 标题与查询词相似时加分
- 能抽取到 GitHub 仓库时加分

对应函数：

- `repoScoreHint()`

### Step 5. 产出结构化结果

写入 `runtimeTask.Metadata["artifact_values"]`：

- `candidate_repositories`
- `repo_validation_report`
- `repo_url`

如果没找到 `repo_url`，仍然会返回一份文本报告，避免节点结果为空白。

## 产物定义

### `candidate_repositories`

当前是 JSON 字符串，包含候选论文及其仓库信息，结构大致如下：

```json
[
  {
    "paper_id": "2510.12323",
    "title": "RAG-Anything: All-in-One RAG Framework",
    "repo_urls": ["https://github.com/HKUDS/RAG-Anything"],
    "source": "papers_with_code(hf)",
    "score_hint": 8
  }
]
```

### `repo_validation_report`

当前是人类可读文本，包含：

- 查询词
- 数据来源
- 候选数量
- 最终选中的 `repo_url`
- 如果没找到，说明原因

### `repo_url`

最终选中的 GitHub 仓库地址。如果没有命中可靠仓库，则为空字符串。

## 当前限制

这次改造虽然已经实现了“真实联网查询”，但还不是最终版，当前限制包括：

### 1. 当前真实查询入口是 HuggingFace Papers API

不是直接请求老的 `paperswithcode.com/api/v1/...`。

原因：

- 当前网络环境下，`paperswithcode.com` 相关页面会跳转
- HuggingFace Papers API 更稳定、可直接返回 JSON

### 2. 还没有真正落 GitHub Search 回退

目前 planner 的节点描述里已经写了：

- `Papers with Code search -> validation/ranking -> fallback GitHub search`

但执行层这一步目前**只做了前半段**：

- 已落地：论文查询 + GitHub URL 抽取
- 未落地：GitHub Search API 回退检索

因此文档上需要明确区分：

- **规划层目标**已经包含 GitHub 回退
- **执行层当前实现**还没有真正调用 GitHub Search API

### 3. 不是所有论文详情都带仓库链接

即使查询成功，也可能拿不到 GitHub 仓库，此时：

- `candidate_repositories` 仍会输出候选论文
- `repo_validation_report` 会说明未找到仓库
- `repo_url` 为空

## 配置项

当前支持以下环境变量：

```bash
PWC_API_BASE_URL=https://huggingface.co
PWC_SEARCH_LIMIT=5
PWC_HTTP_TIMEOUT=8s
```

## 本次改动价值

- 将 `repo_discovery` 从“LLM 猜仓库链接”升级为“真实联网查询节点”
- 让论文复现 DAG 的仓库定位步骤更稳定、更可解释
- 为后续补充 GitHub Search 回退、仓库可访问性校验、自动 clone 打下基础

## 后续建议

建议下一步继续补 3 个能力：

1. GitHub Search API 回退
2. 对候选仓库做更严格校验
   - 是否公开
   - README 是否包含论文标题/方法名
   - 是否包含 `requirements.txt` / `environment.yml` / `setup.py`
3. 在 `repo_prepare` 节点里真正消费 `repo_validation_report`，而不只是消费 `repo_url`

