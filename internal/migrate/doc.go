// [S1] migrate：只读 inventory、固定 manifest 与原子发布的冷导入 host。
//
// 允许依赖：mdfile、segment；typed-edge、Claim、review、codec 与 hash 由 shared core 校验。
package migrate
