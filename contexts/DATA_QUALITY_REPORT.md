# Spider Dataset Data Quality Report

本报告由 Rich Context 自动分析生成，汇总了 Spider dev 集涉及的 20 个数据库中发现的数据质量问题。这些问题是导致 Text2SQL 系统产生"看似错误但实际合理"结果的主要原因之一，也是本项目对 Spider 数据集进行校准（修正 221 条标注）的重要依据。

## 问题总览

在 20 个数据库中，**16 个存在数据质量问题**，共发现 **55 处质量标注**。问题可归为以下五类：

| 问题类型 | 出现次数 | 典型表现 |
|---------|---------|---------|
| TEXT 存数值 | 18 | 数值字段声明为 TEXT，需 `CAST()` 才能正确比较排序 |
| 空格问题 | 9 | 字段值含前导/尾随空格，需 `TRIM()` 匹配 |
| NULL / 空字符串 | 10 | 用空字符串代替 NULL，或整列 100% NULL |
| 孤儿记录 / 外键缺失 | 6 | 外键引用不存在的主键记录 |
| Schema 设计问题 | 5 | 列名与实际语义不符、拼写错误、日期逻辑反转等 |

## 各数据库详细质量标注

### wta_1 (7 issues)

WTA 网球数据集，问题最密集。

| 表 | 字段 | 问题 |
|---|------|------|
| players | first_name | 含前导/尾随空格，需 `TRIM()` |
| players | birth_date | 518 条空字符串代替 NULL（2.5%），部分值为 1970 年占位数据 |
| players | hand | 954 条空字符串代替 NULL（4.6%） |
| matches | score | 含尾随空格 |
| rankings | ranking_date | INTEGER 存 YYYYMMDD 格式日期，易被误解为 Unix 时间戳 |
| rankings | - | 存在完全重复的记录（相同 date/player_id/points/tours） |
| rankings | player_id | 1 条孤儿记录（player_id 不在 players 表中） |

### tvshow (7 issues)

TV 节目数据集，类型滥用严重。

| 表 | 字段 | 问题 |
|---|------|------|
| Cartoon | Channel | TEXT 存数值频道号 |
| Cartoon | Original_air_date | TEXT 存日期，格式为 "November14,2008"（无空格分隔） |
| TV_Channel | id | TEXT 存数值 |
| TV_series | Channel | TEXT 存数值 |
| TV_series | Rating | TEXT 存数值 |
| TV_series | Viewers_m | TEXT 存数值 |
| TV_series | id | 主键用 REAL 类型而非 INTEGER，可能有精度问题 |

### flight_2 (6 issues)

航班数据集，**空格问题最严重**，导致大量 JOIN 失败。

| 表 | 字段 | 问题 |
|---|------|------|
| airports | AirportName | 含尾随空格 |
| airports | City | 含尾随空格 |
| airports | CountryAbbrev | 含尾随空格 |
| flights | SourceAirport | 含前导空格（3 字母代码前有空格） |
| flights | DestAirport | 含前导空格 |
| flights ↔ airports | - | **1200 条孤儿记录**：航班的 SourceAirport/DestAirport 不在 airports 表中 |

### student_transcripts_tracking (6 issues)

学生成绩追踪数据集，大量未使用字段。

| 表 | 字段 | 问题 |
|---|------|------|
| Addresses | line_3 | 100% NULL，未使用 |
| Addresses | zip_postcode | TEXT 存纯数字邮编 |
| Degree_Programs | other_details | 100% NULL |
| Sections | other_details | 100% NULL |
| Semesters | other_details | 100% NULL |
| Student_Enrolment | other_details | 100% NULL |

### concert_singer (5 issues)

音乐会歌手数据集，外键类型不匹配。

| 表 | 字段 | 问题 |
|---|------|------|
| concert | Stadium_ID | TEXT 存数值 |
| concert | Year | TEXT 存数值 |
| singer | Song_release_year | TEXT 存数值 |
| singer ↔ singer_in_concert | Singer_ID | **类型不匹配**：singer 表为 INT，singer_in_concert 表为 TEXT，JOIN 需 CAST |
| singer_in_concert | Singer_ID | TEXT 存数值 |

### employee_hire_evaluation (4 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| evaluation | Employee_ID | TEXT 存数值 |
| evaluation | Year_awarded | TEXT 存数值 |
| hiring | Start_from | TEXT 存数值 |
| shop | District | **列名误导**：名为 "District" 实际存储的是体育场/场馆名称 |

### cre_Doc_Template_Mgt (3 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| Documents | Other_Details | 100% NULL（15/15 条） |
| Templates | Date_Effective_To | **50% 记录的结束日期早于开始日期**，日期逻辑反转 |
| Templates | Template_Details | 100% 空字符串（非 NULL） |

### car_1 (3 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| car_names | Model | 含前导/尾随空格 |
| car_names | - | 1 条孤儿记录（Model 不在 model_list 中） |
| model_list | - | 1 条孤儿记录（Maker ID 不在 car_makers 中） |

### museum_visit (3 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| museum | Open_Year | TEXT 存数值 |
| visit | visitor_ID | TEXT 存数值 |
| visitor | Name | 含前导/尾随空格 |

### dog_kennels (2 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| Charges | charge_type | 3 条孤儿记录（charge_type 不在 Treatment_Types 中） |
| Owners | zip_code | TEXT 存 5 位数字邮编，含前导零（如 "00589"） |

### battle_death (2 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| ship | tonnage | TEXT 存数值，且含一个非数值 "t"（需特殊处理） |
| death | note | 无显著质量问题 |

### orchestra (2 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| performance | Share | TEXT 存百分比值（如 "22.7%"），需 `REPLACE` + `CAST` |
| performance | Weekly_rank | TEXT 存数值 |

### real_estate_properties (2 issues)

| 表 | 字段 | 问题 |
|---|------|------|
| Ref_Feature_Types | feature_type_name | 拼写错误："Securiyt" 应为 "Security" |
| Other_Property_Features | property_feature_description | 无显著问题 |

### voter_1 (1 issue)

| 表 | 字段 | 问题 |
|---|------|------|
| CONTESTANTS | contestant_name | 第 11 条记录姓名拼接无分隔符："Loraine NygrenTania Mattioli" |

### world_1 (1 issue)

| 表 | 字段 | 问题 |
|---|------|------|
| city | District | 4 条记录为空字符串（0.1%） |

### course_teach (1 issue)

| 表 | 字段 | 问题 |
|---|------|------|
| teacher | Age | TEXT 存数值 |

## 对 Text2SQL 评估的影响

这些数据质量问题直接影响 Text2SQL 评估的公平性：

1. **空格问题**使得精确匹配的 SQL（如 `WHERE City = 'Aberdeen'`）无法命中实际数据为 `'Aberdeen '` 的记录。系统若生成 `TRIM(City) = 'Aberdeen'` 反而被判为"错误"。

2. **TEXT 存数值**导致排序语义不同：`ORDER BY '9' > '10'`（字符串序），而 `ORDER BY 9 < 10`（数值序）。

3. **孤儿记录**使得 INNER JOIN 和 LEFT JOIN 产生不同行数，而标准答案往往未考虑这一点。

这是本项目对 Spider dev 数据集进行系统校准（修正 221 条标注错误，占 21.4%）的核心动机之一。校准后的数据集位于 `benchmarks/spider_corrected/`。

## 如何利用本报告

- **复现实验**：使用 `benchmarks/spider_corrected/` 中的校准数据集，而非 Spider 原始标注
- **查看完整 Rich Context**：每个数据库的 Rich Context 文件位于 `contexts/sqlite/spider/`，包含更详细的字段语义、JOIN 路径等信息
- **生成新的 Rich Context**：运行 `go run ./cmd/gen_rich_context_spider --config dbs/spider/<db>.json` 可为任意数据库生成完整的 Rich Context 分析
