# v1.17.0 软件专项用例

本表复用 schema 3 的用例 ID，并在本版明确其软件验证含义；不等同于历史版本同 ID 的整板现场流程。
所有输入来自仓库回归夹具或临时测试状态。完整设计与现场 EDA 验收按本次用户决定延期。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | CLI、输入模型与动作权限 | 运行 Go 与协议回归，核对 compose schema、命令目录及类型化动作 scope/effect/confirmation/verification/batch；未知输入和不匹配契约必须拒绝。 |
| F1 | 几何读取与 NC 定位 | 运行 geometry、budget 和 actions 夹具；只使用当前页几何，NC 唯一定位，marker 保留完整读取；分别验证写前失败和已写入后回读失败的 mutationApplied/partial。 |
| F2 | 中文多行文本创建 | 运行 schematic-text-create 与 CLI/协议测试；校验长度和几何，创建只执行一次，通过新 getAll 精确 ID 回读；不确定写入不得自动重试或删除。 |
| E1 | 清页和受保护文字 | 运行 preserve、native-attributes、attribute-move 和图签夹具；保留物理引脚与对象属性归属，必填图签不能靠隐藏绕过，文字移动只改经校验的 x/y。 |
| L1 | daemon、传输与诊断 | 运行 owned/isolated、transport-background/FIFO 和 connector-logs 回归；真实本机子进程核验 EOF、父进程崩溃、实例隔离、占用端口及错误身份拒绝。 |
| N1 | 安装、更新与 Skill | 运行 selfupdate/local、Skill read、发布脚本和跨平台 native install CI；要求路径归一及越界保护、真实正文/版本/SHA、临时替换与离线命令可用。 |
| R1 | 开发候选制品核验 | 构建 dev.3 五平台资产；核对 build info 和 SHA256、15 项本机 CLI 冒烟、首次安装及 v1.16.0 升级的 CLI/192 个 Skill 文件逐字节一致和旧文件清理。 |
| E2E | 完整设计与现场验收 | 未执行；后续按原始最小点灯板需求完成设计，并补本版几何读取、文本创建、NC、保存、重载及新回读。不能从自动测试或上传成功补签通过。 |

复现入口：`go test ./...`、`go vet ./...`、连接器 `npm test`/typecheck/build、MCP 测试、
`make agent-check skill-check release-script-test`，以及固定候选的构建、`release-smoke.py --local-dev` 和隔离安装。
正式 v1.17.0 制品另用 `make release-check release-build release-smoke VERSION=v1.17.0` 核验；
发布后还须对真实 GitHub 资产下载、首次安装和升级进行验证，不复用本地传输替身结论。
