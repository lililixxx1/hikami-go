package publisher

import (
	"hikami-go/internal/biliutil"
)

// 将 biliutil 中的类型和函数重新导出，保持对外接口不变。
type BiliCookie = biliutil.BiliCookie

var LoadCookie = biliutil.LoadCookie
var ErrCookieMissing = biliutil.ErrCookieMissing
