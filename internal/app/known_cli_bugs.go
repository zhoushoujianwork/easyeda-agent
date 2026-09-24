package app

// Advisories stay on stderr and do not retry, suppress errors, or alter the raw
// connector response. Remove each when its linked issue is resolved and verified.
const (
	knownBugSchModify = "warning [bug #256]: Web 4.1.60 原理图修改曾间歇报 cmdKey，触发条件未明；失败后先 fresh sch list 核对目标字段，勿盲重试。https://github.com/zhoushoujianwork/easyeda-agent/issues/256"
	knownBugNCClear   = "warning [bug #257]: Web 4.1.60 NC 清除曾间歇未生效；用 sch list --include-pins 核对指定引脚 noConnected:false，关键结果保存重载复核；失败后停止依赖步骤。https://github.com/zhoushoujianwork/easyeda-agent/issues/257"
	knownBugPCBConfig = "warning [bug #258]: Web 4.1.60 首次将系统规则转为自定义配置时，曾出现未请求孔间距微小变化；先保留 config get 快照，核对完整差分及保存重载后的配置，verified:false 仍为失败。https://github.com/zhoushoujianwork/easyeda-agent/issues/258"
)
