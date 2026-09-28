---
name: easyeda-repo-maintain
description: "维护 easyeda-agent 仓库的 Skill、CLI、daemon、connector 与文档，补回可复用经验并组织验证和 Git 贡献。 Develop and contribute to the linked automation repository; use easyeda-agent for circuit design operations."
---

# EasyEDA 仓库维护

执行 `python3 <此 Skill 目录>/scripts/repo-root.py` 获取源 checkout；不要在调用方项目里
创建一套 easyeda-agent。先读该仓库 `AGENTS.md`、目标目录约定与 `docs/README.md`，检查分支、
工作树和相关 diff，保留用户已有改动。仓库失效时修正安装入口，不猜个人目录。

1. 用 `docs/README.md` 的归属表确定事实的唯一维护位置。引入概念先更新 `docs/concepts.md`；
   改设计工作流先读 `.agents/skills/README.md` 和 `docs/skill-design.md`。通用知识回填公开 Skill 的
   相应 reference，长期设计说明留在 `docs/`；执行流水留在本地忽略的 `artifacts/`，
   不为每次运行新增报告，不复制同一规则到多个入口。入库边界遵守 `AGENTS.md` 的开源资料规则。
2. 先明确 Skill 的输入、命令和回读，再开发基础设施。新增功能按 `docs/cli-design.md` 设计
   Cobra 子命令与 typed action；官方 API 先查 `docs/ecosystem-survey.md` 及离线类型/fixture。
   缺能力时修工具，现场操作约束以 `AGENTS.md` 和公开 Skill 为准。
3. 回填样例时记录来源、开始状态、可调参数及单位、实际步骤、结果、错误修法和验证状态。
   最小缺陷复现放入对应代码的 `testdata/` 并接入自动测试；发布证据按发布目录和冻结清单维护。
   外部项目的客户信息、绝对路径、凭据和原始私有日志留在外部；只贡献公开依据或去标识化经验。
4. 按变更选择验证：协作入口运行 `make agent-check`；公开 Skill 运行 `make skill-check`；
   Go 运行相关测试及 `make test`；connector 运行其 typecheck、test 和 build。现场回归触发条件
   与原始需求输入遵守 `AGENTS.md`，离线成功不能代替现场验收。改底层后同步公开 Skill 说明。
5. Git 分支、提交、推送、主分支集成与发布均遵循 `AGENTS.md` 和本次用户范围。不要将调用
   此 Skill 本身解释成推送或发布授权；已有授权内完成工作，不反复询问。集成时核对差异，
   不夹带其他工作，不为了整理分支丢弃提交。

交付写明具体变化、commit、运行过的验证、尚未覆盖的现场行为和知识落点。使用当前环境的
PR 附件能力记录实际创建或更新的 PR。无需修改时报告查询结论，不制造空提交。
