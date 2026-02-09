# ReAct SQL — Text2SQL Experiment Toolkit

<p align="center">
  <img src="pics/rc_gen.png" width="680" alt="ReAct SQL Rich Context Generation" />
</p>

A **Text2SQL** experiment framework built on the **ReAct paradigm** and **Rich Context**. Achieves **94.39%** execution accuracy (EX) on the calibrated Spider 1.0 dev set.

This repository provides a complete experiment reproduction toolchain, including Rich Context generation, Text2SQL evaluation, and result analysis.

<details>
<summary>🇨🇳 中文说明</summary>

基于 **ReAct 范式**和 **Rich Context** 的 Text2SQL 实验框架。在校准后的 Spider 1.0 dev 数据集上达到 **94.39%** 执行准确率 (EX)。

本仓库提供完整的实验复现工具链，包括 Rich Context 生成、Text2SQL 评估、结果分析等。

</details>

## Quick Start

```bash
# 1. Clone the repo
git clone <repo-url> && cd ReActSqlExp

# 2. Download datasets (Spider databases + BIRD databases)
bash scripts/download_datasets.sh

# 3. Configure LLM
cp llm_config.json.example llm_config.json
# Edit llm_config.json and fill in your API Key

# 4. Generate Rich Context (e.g. for a Spider database)
go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json

# 5. Run evaluation
go run ./cmd/eval_spider --use-rich-context --use-react
```

## Prerequisites

- **Go** >= 1.21
- **LLM API**: Any OpenAI-compatible model (DeepSeek-V3, Qwen-3 Max, GLM-4.7, Kimi-K2, etc.)
- **curl** (or wget) + **unzip**: For dataset download
- **gdown** (recommended): `pip install gdown`, for reliable Google Drive downloads

## Project Structure

```
ReActSqlExp/
├── cmd/                              # CLI entry points
│   ├── eval_spider/                  # Spider dataset evaluation
│   ├── eval_bird/                    # BIRD dataset evaluation
│   ├── gen_rich_context_spider/      # Spider Rich Context generation
│   ├── gen_rich_context_bird/        # BIRD Rich Context generation
│   ├── gen_all_dev/                  # Batch Rich Context generation (all dev DBs)
│   ├── extract_result_fields/        # Extract result field descriptions from Gold SQL
│   └── analyze_results/              # Result analyzer
├── internal/                         # Core libraries
│   ├── adapter/                      # Database adapters (SQLite/MySQL/PostgreSQL)
│   ├── agent/                        # Multi-Agent system (Coordinator + Worker)
│   ├── context/                      # Rich Context management
│   ├── inference/                    # Text2SQL inference pipeline (ReAct loop)
│   ├── llm/                          # LLM configuration
│   └── logger/                       # Logging utilities
├── benchmarks/                       # Datasets
│   ├── spider/                       # Spider original (database/ needs download)
│   ├── spider_corrected/             # Calibrated Spider dev set (with field descriptions)
│   └── bird/                         # BIRD dataset (dev_databases/ needs download)
├── contexts/                         # Rich Context (20 Spider + 2 BIRD examples)
│   ├── DATA_QUALITY_REPORT.md        # Spider data quality analysis report
│   └── sqlite/
│       ├── spider/                   # Spider database Rich Contexts
│       └── bird/                     # BIRD database Rich Contexts
├── results/                          # Experiment results
│   └── spider/qwen-final/           # Spider final results (94.39% EX)
├── dbs/spider/                       # Spider database configs (166 DBs)
├── scripts/
│   ├── download_datasets.sh          # One-click dataset download
│   ├── stash_data.sh                 # Stash data (simulate fresh clone)
│   └── restore_data.sh              # Restore stashed data
├── llm_config.json.example           # LLM config template
└── dbs/sqlite/                       # SQLite database config examples
```

## Configuration

### LLM Configuration

Copy the template and fill in your API Key:

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

Any OpenAI-compatible model is supported. `llm_config.json` is placed at the project root and is included in `.gitignore`.

<details>
<summary>🇨🇳 中文</summary>

支持任何 OpenAI 兼容接口的模型。`llm_config.json` 放在项目根目录，已加入 `.gitignore`。

</details>

### Dataset Download

```bash
bash scripts/download_datasets.sh
```

This script downloads two database directories (requires wget + unzip):
- **Spider 1.0 databases** (~840MB) → `benchmarks/spider/database/`
- **BIRD dev databases** (~1.4GB) → `benchmarks/bird/dev/dev_databases/`

The following are **already included in the repo** — no extra download needed:
- Calibrated Spider dev set (221 annotation fixes): `benchmarks/spider_corrected/`
- 20 Spider + 2 BIRD Rich Context examples: `contexts/sqlite/`
- Spider data quality report: `contexts/DATA_QUALITY_REPORT.md`
- Spider database configs (166): `dbs/spider/`

## Experiment Pipeline

### Step 1: Generate Rich Context

Rich Context is the core of this method. A multi-agent system automatically analyzes database structure and generates structured context including field semantics, JOIN paths, data characteristics, and more.

<details>
<summary>🇨🇳 中文</summary>

Rich Context 是本方法的核心，通过多 Agent 系统自动分析数据库结构，生成包含字段语义、JOIN 路径、数据特征等的结构化上下文。

</details>

**Spider:**

```bash
# Single database
go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json

# Use a different model
go run ./cmd/gen_rich_context_spider --v3.2 --config dbs/spider/concert_singer.json

# Batch: all dev databases (with Docker-style progress bar)
go run ./cmd/gen_all_dev --benchmark spider --workers 4
```

**BIRD:**

```bash
# Single database
go run ./cmd/gen_rich_context_bird --db card_games

# Batch (3 concurrent workers)
go run ./cmd/gen_all_dev --benchmark bird --workers 3

# Skip existing
go run ./cmd/gen_all_dev --benchmark bird --workers 3
```

Generated Rich Contexts are saved to `contexts/sqlite/spider/` and `contexts/sqlite/bird/`.

### Step 2: Extract Result Field Descriptions (Optional)

Extract query result fields and descriptions from Gold SQL for field-alignment evaluation:

```bash
go run ./cmd/extract_result_fields \
  --input benchmarks/spider/dev.json \
  --output benchmarks/spider/dev_with_fields.json
```

Output format example:

```json
{
  "db_id": "concert_singer",
  "question": "Show name, country, age for all singers ordered by age from the oldest to the youngest.",
  "query": "SELECT name, country, age FROM singer ORDER BY age DESC",
  "result_fields": ["name", "country", "age"],
  "result_fields_description": "name: Singer's full name; country: Singer's country of origin; age: Singer's current age in years"
}
```

### Step 3: Run Evaluation

**Spider:**

```bash
# Baseline (no Rich Context, no ReAct)
go run ./cmd/eval_spider

# With Rich Context
go run ./cmd/eval_spider --use-rich-context

# With ReAct loop
go run ./cmd/eval_spider --use-react

# Full config (Rich Context + ReAct)
go run ./cmd/eval_spider --use-rich-context --use-react

# Specify model
go run ./cmd/eval_spider --v3.2 --use-rich-context --use-react

# Specify range (for debugging)
go run ./cmd/eval_spider --start 0 --end 100 --use-rich-context --use-react

# Field clarification mode
go run ./cmd/eval_spider --use-rich-context --use-react --clarify force
```

**BIRD:**

```bash
# Full evaluation
go run ./cmd/eval_bird --use-rich-context --use-react

# Filter by difficulty
go run ./cmd/eval_bird --difficulty simple --use-rich-context

# Limit number of queries
go run ./cmd/eval_bird --limit 100 --use-rich-context
```

### Step 4: Analyze Results

```bash
go run ./cmd/analyze_results --input results/spider/<your-result-dir>/results.json
```

The analyzer automatically classifies results (exact match, semantic equivalence, row count errors, data inconsistencies, etc.) and generates statistical reports.

## Utility Scripts

```bash
# Stash all data to .data_stash/ (simulate a fresh clone, idempotent)
bash scripts/stash_data.sh

# Restore stashed data (idempotent)
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

This project systematically calibrated the Spider dev dataset, correcting **221 annotation errors** (21.4% of total samples), including ambiguous queries, labeling mistakes, and data quality issues. The calibrated dataset is at `benchmarks/spider_corrected/`. See `contexts/DATA_QUALITY_REPORT.md` for the detailed data quality analysis.

<details>
<summary>🇨🇳 中文</summary>

本项目对 Spider dev 数据集进行了系统校准，修正了 **221 个标注错误**（占总样本的 21.4%），包括歧义查询、标注错误、数据质量问题等。校准后的数据集位于 `benchmarks/spider_corrected/`。详细的数据质量分析见 `contexts/DATA_QUALITY_REPORT.md`。

</details>

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
| `--model` | Model type | `deepseek-v3` |
| `--use-rich-context` | Enable Rich Context | `false` |
| `--use-react` | Enable ReAct loop | `false` |
| `--difficulty` | Filter by difficulty | all |
| `--limit` | Max number of queries | `0` (all) |

### gen_all_dev

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--benchmark` | Benchmark name (`spider` / `bird`) | *required* |
| `--workers` | Number of concurrent workers | `2` |
| `--v3.2` | Use DeepSeek-V3.2 | `false` |

## License

MIT License
