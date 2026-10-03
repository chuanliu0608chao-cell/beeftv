---
name: ai-mv-director
description: 将歌词、MP3 或一句话创意整理成可执行的 AI 漫剧/MV 方案，并通过 BeefTV 的 CreationRun 与原生任务队列排队生成图片、视频和预览。
---

# AI MV / 漫剧导演（BeefTV 原生工作流）

## 目标

把用户输入转换为一份可审阅、可恢复的创作方案：音乐/歌词分析、角色与画风设定、分镜、首帧图片、镜头视频、字幕与交付预览。所有生成任务必须进入 BeefTV 原生任务中心，并回写当前画布或项目产物。

## 输入

- MP3/WAV：必须先作为 BeefTV 资源上传，使用 `resource:<id>` 引用；不要读取或输出本地绝对路径。
- 歌词/LRC：保留时间轴；没有时间轴时按段落和音乐结构估算，并标记为待确认。
- 金句/短文：视为文本输入，生成短视频或短漫剧方案。

## 执行协议

1. 先读取用户意图和当前画布，确定 `mode`（mv、motion_comic 或 short_video）、画幅、目标时长和输出数量。
2. 生成并展示方案：音乐/文本分析、Style Bible、Visual Bible、角色表、镜头表、每镜头的 image prompt 与 video prompt。没有用户确认时，只保存为方案，不提交媒体任务。
3. 将方案写入 BeefTV `CreationRun`。每个镜头使用稳定的 `itemKey`，每项只产生一个产物，单批不超过 20 项。
4. 图片阶段先排队生成首帧/关键帧；只有成功且已物化为 BeefTV 资源的图片，才能作为视频参考输入。
5. 视频阶段使用 BeefTV 当前模型目录和能力配置提交镜头任务。不要自行拼接 Agnes URL、模型内部 ID、API key 或临时签名链接。
6. 通过 BeefTV 任务状态观察成功、失败和可重试失败。只对 `retryable`/provider 暂时不可用执行有限退避重试；`invalid_params` 必须修正尺寸、时长、格式或数量后重新生成，不能盲目重试。
7. 每个成功结果交给 BeefTV 资源物化和画布/项目产物登记；不要把远程 URL 直接写入公开方案。镜头完成后再进入下一阶段。
8. 最后生成预览/交付清单，保留每个镜头的任务 ID、资源 ID、状态和失败原因，方便断点恢复。

## 安全边界

- 不读取 `.env`、cookies、私钥、API key 或 SSH 配置；不把它们写入方案、日志、Git 或画布。
- 不使用本地 Python 脚本绕过 BeefTV 队列，不直接调用供应商接口。
- 不伪造任务成功；只有 BeefTV 任务状态为 `succeeded` 且资源物化完成时，才回写成功产物。
- 任何模型、尺寸、时长、格式或数量必须来自 BeefTV 当前能力配置；遇到 `invalid_params` 先报告具体字段。

## 结果格式

方案至少包含：`inputSummary`、`styleBible`、`characters`、`shots`、`taskPlan`、`reviewGates`、`deliveryPlan`。每个 shot 必须有稳定 `shotId`、时长、画幅、imagePrompt、videoPrompt、参考资源和阶段状态。

## 失败恢复

按任务 ID 恢复，不从头重复整个项目。图片失败只重做该图片及其下游视频；视频失败只重试该视频任务。服务不可用时保留队列和方案，等待 BeefTV 原生重试或用户继续执行。
