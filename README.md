# ReAct SQL - Text2SQL Experiment Toolkit

基于 **ReAct 范式**和 **Rich Context** 的 Text2SQL 实验框架。在校准后的 Spider 1.0 dev 数据集上达到 **94.39%** 执行准确率 (EX)。

本仓库提供完整的实验复现工具链，包括 Rich Context 生成、Text2SQL 评估、结果分析等。

## Quick Start

```bash
# 1. 克隆仓库
git clone <repo-url> && cd ReActSqlExp

# 2. 下载数据集（Spider databases + BIRD databases）
bash scripts/download_datasets.sh

# 3. 配置 LLM
cp llm_config.json.example llm_config.json
# 编辑 llm_config.json，填入你的 API Key

# 4. 生成 Rich Context（以 Spider 某个数据库为例）
go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json

# 5. 运行评估
go run ./cmd/eval_spider --use-rich-context --use-react
```

## Prerequisites

- **Go** >= 1.21
- **LLM API**: 支持 OpenAI 兼容接口的模型（DeepSeek-V3, Qwen-3 Max, GLM-4.7, Kimi-K2 等）
- **curl**（或 wget）+ **unzip**：用于下载数据集

## Project Structure

```
ReActSqlExp/
├── cmd/                              # 命令行工具入口
│   ├── eval_spider/                  # Spider 数据集评估
│   ├── eval_bird/                    # BIRD 数据集评估
│   ├── gen_rich_context_spider/      # Spider Rich Context 生成
│   ├── gen_rich_context_bird/        # BIRD Rich Context 生成
│   ├── extract_result_fields/        # 从 Gold SQL 提取结果字段描述
│   └── analyze_results/              # 评估结果分析器
├── internal/                         # 核心库代码
│   ├── adapter/                      # 数据库适配器（SQLite/MySQL/PostgreSQL）
│   ├── agent/                        # 多 Agent 系统（Coordinator + Worker）
│   ├── context/                      # Rich Context 管理
│   ├── inference/                    # Text2SQL 推理管线（ReAct 循环）
│   ├── llm/                          # LLM 配置管理
│   └── logger/                       # 日志工具
├── benchmarks/                       # 数据集
│   ├── spider/                       # Spider 原始数据集（database/ 需下载）
│   ├── spider_corrected/             # 校准后的 Spider dev 集（已含字段描述）
│   └── bird/                         # BIRD 数据集（dev_databases/ 需下载）
├── contexts/                         # Rich Context（含 20 个 Spider + 2 个 BIRD 示例）
│   ├── DATA_QUALITY_REPORT.md        # Spider 数据质量分析报告
│   └── sqlite/
│       ├── spider/                   # Spider 数据库的 Rich Context
│       └── bird/                     # BIRD 数据库的 Rich Context
├── results/                          # 实验结果
│   └── spider/qwen-final/            # Spider 最终结果 (94.39% EX)
├── dbs/spider/                       # Spider 数据库配置（166 个库）
├── scripts/
│   ├── download_datasets.sh          # 一键下载数据集
│   ├── stash_data.sh                 # 暂存数据（模拟全新环境）
│   └── restore_data.sh              # 恢复暂存数据
├── llm_config.json.example           # LLM 配置示例
└── dbs/sqlite/                       # SQLite 数据库配置示例
```

## Configuration

### LLM 配置

复制模板并填入 API Key：

```bash
cp llm_config.json.example llm_config.json
```

```json
{
  "deepseek_v3": {
    "model_name": "deepseek-v3-250324",
    "token": "YOUR_TOKEN_HERE",
    "base_url": "https://ark.cn-beijing.volces.com/api/v3"
  },
  "deepseek_v3_2": {
    "model_name": "deepseek-v3-2-251201",
    "token": "YOUR_TOKEN_HERE",
    "base_url": "https://ark.cn-beijing.volces.com/api/v3"
  }
}
```

支持任何 OpenAI 兼容接口的模型。`llm_config.json` 放在项目根目录，已加入 `.gitignore`。

### 数据集下载

```bash
bash scripts/download_datasets.sh
```

该脚本会下载两个数据库文件目录（需 wget + unzip）：
- **Spider 1.0 databases** (~840MB) -> `benchmarks/spider/database/`
- **BIRD dev databases** (~1.4GB) -> `benchmarks/bird/dev/dev_databases/`

以下内容**已包含在仓库中**，无需额外下载：
- 校准后的 Spider dev 集（221 条标注修正）：`benchmarks/spider_corrected/`
- 20 个 Spider + 2 个 BIRD 的 Rich Context 示例：`contexts/sqlite/`
- Spider 数据质量分析报告：`contexts/DATA_QUALITY_REPORT.md`
- Spider 数据库配置文件（166 个）：`dbs/spider/`

## Experiment Pipeline

### Step 1: 生成 Rich Context

Rich Context 是本方法的核心，通过多 Agent 系统自动分析数据库结构，生成包含字段语义、JOIN 路径、数据特征等的结构化上下文。

**Spider 数据集：**

```bash
# 单个数据库
go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json

# 使用其他模型
go run ./cmd/gen_rich_context_spider --v3.2 --config dbs/spider/concert_singer.json

# 批量生成
for config in dbs/spider/*.json; do
  go run ./cmd/gen_rich_context_spider --config "$config"
done
```

**BIRD 数据集：**

```bash
# 单个数据库
go run ./cmd/gen_rich_context_bird --db card_games

# 批量（3 并发）
go run ./cmd/gen_rich_context_bird --workers 3

# 跳过已存在的
go run ./cmd/gen_rich_context_bird --workers 3 --skip-existing
```

生成的 Rich Context 保存在 `contexts/sqlite/spider/` 和 `contexts/sqlite/bird/`。

### Step 2: 提取结果字段描述（可选前置步骤）

从 Gold SQL 中提取查询应返回的字段及描述，用于字段对齐评估：

```bash
go run ./cmd/extract_result_fields \
  --input benchmarks/spider/dev.json \
  --output benchmarks/spider/dev_with_fields.json
```

输出格式示例：

```json
{
  "db_id": "concert_singer",
  "question": "Show name, country, age for all singers ordered by age from the oldest to the youngest.",
  "query": "SELECT name, country, age FROM singer ORDER BY age DESC",
  "result_fields": ["name", "country", "age"],
  "result_fields_description": "name: Singer's full name; country: Singer's country of origin; age: Singer's current age in years"
}
```

### Step 3: 运行评估

**Spider 评估：**

```bash
# Baseline（无 Rich Context，无 ReAct）
go run ./cmd/eval_spider

# 使用 Rich Context
go run ./cmd/eval_spider --use-rich-context

# 使用 ReAct 循环
go run ./cmd/eval_spider --use-react

# 完整配置（Rich Context + ReAct）
go run ./cmd/eval_spider --use-rich-context --use-react

# 指定模型
go run ./cmd/eval_spider --v3.2 --use-rich-context --use-react

# 指定范围（调试用）
go run ./cmd/eval_spider --start 0 --end 100 --use-rich-context --use-react

# 字段澄清模式
go run ./cmd/eval_spider --use-rich-context --use-react --clarify force
```

**BIRD 评估：**

```bash
# 完整评估
go run ./cmd/eval_bird --use-rich-context --use-react

# 按难度过滤
go run ./cmd/eval_bird --difficulty simple --use-rich-context

# 限制数量
go run ./cmd/eval_bird --limit 100 --use-rich-context
```

### Step 4: 分析结果

```bash
go run ./cmd/analyze_results --input results/spider/<your-result-dir>/results.json
```

分析器会自动分类结果（精确匹配、语义等价、行数错误、数据不一致等）并生成统计报告。

## Utility Scripts

```bash
# 暂存所有数据到 .data_stash/（模拟全新克隆环境，幂等）
bash scripts/stash_data.sh

# 恢复暂存的数据（幂等）
bash scripts/restore_data.sh
```

## Key Results

### Spider 1.0 dev (Corrected, 1034 queries)

| Method | Base Model | EX (%) | Syntax Error (%) |
|--------|-----------|--------|-----------------|
| DAIL-SQL + GPT-4 | GPT-4 | 86.6 | - |
| DIN-SQL + GPT-4 | GPT-4 | 85.3 | - |
| **ReAct SQL (Ours)** | **Qwen-3 Max** | **94.39** | **0.00** |

### Ablation: Rich Context Modes

| Mode | EX (%) |
|------|--------|
| Mode 1: Schema only | 85.20 |
| Mode 2: + Descriptions | 88.30 |
| Mode 3: + Index info | 88.12 |
| Mode 4: + JOIN paths | 92.75 |
| Mode 5: Full Rich Context | **94.39** |

### Multi-Model Results

| Base Model | Baseline (One-shot) | ReAct SQL |
|-----------|-------------------|-----------|
| DeepSeek-V3 | 78.24% | 93.82% |
| Qwen-3 Max | 77.56% | **94.39%** |
| GLM-4.7 | 76.31% | 92.94% |
| Kimi-K2 | 75.87% | 92.36% |

### Dataset Calibration

本项目对 Spider dev 数据集进行了系统校准，修正了 **221 个标注错误**（占总样本的 21.4%），包括歧义查询、标注错误、数据质量问题等。校准后的数据集位于 `benchmarks/spider_corrected/`。详细的数据质量分析见 `contexts/DATA_QUALITY_REPORT.md`。

## Eval Parameters Reference

### eval_spider

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--dev` | dev.json path | `benchmarks/spider_corrected/dev_with_field_with_id.json` |
| `--db-dir` | Database directory | `benchmarks/spider/database` |
| `--context-dir` | Rich Context directory | `contexts/sqlite/spider` |
| `--output-dir` | Results output directory | `results/spider` |
| `--use-rich-context` | Enable Rich Context | `false` |
| `--use-react` | Enable ReAct loop | `false` |
| `--react-linking` | Schema linking in ReAct | `false` |
| `--clarify` | Clarification mode (off/on/force) | `off` |
| `--start` | Start index | `0` |
| `--end` | End index (-1 for all) | `-1` |
| `--v3.2` | Use DeepSeek-V3.2 | `false` |

### eval_bird

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--dev` | dev.json path | `benchmarks/bird/dev/dev.json` |
| `--db-dir` | Database directory | `benchmarks/bird/dev/dev_databases` |
| `--context-dir` | Rich Context directory | `contexts/sqlite/bird` |
| `--model` | 模型类型 | `deepseek-v3` |
| `--use-rich-context` | Enable Rich Context | `false` |
| `--use-react` | Enable ReAct loop | `false` |
| `--difficulty` | Filter by difficulty | all |
| `--limit` | Max number of queries | `0` (all) |

## License

MIT License
