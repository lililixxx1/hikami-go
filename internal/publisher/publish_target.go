package publisher

import (
	"encoding/json"
	"strings"
)

// PublishTarget 描述一场已发布专栏的落库标识，序列化为 JSON 字符串存入 sessions.publish_target。
// 持久化 dyn_type/dyn_rid 是为了支撑后续「删除已发布专栏」——B 站 operate/remove 接口
// dyn_id_str 必填，dyn_type/rid_str 可选但建议带上以提高成功率。
type PublishTarget struct {
	DynID   string `json:"dyn_id,omitempty"`   // dyn_id_str：已发布专栏的动态 ID（删除主键）
	DynType int64  `json:"dyn_type,omitempty"` // dyn_type：动态类型
	DynRid  string `json:"dyn_rid,omitempty"`  // rid_str：关联资源 ID
	DraftID string `json:"draft_id,omitempty"` // 草稿模式（未真正发布）的草稿 ID
}

// Marshal 将 PublishTarget 序列化为 JSON 字符串以存入 publish_target 列。
func (t PublishTarget) Marshal() string {
	b, err := json.Marshal(t)
	if err != nil {
		// 结构体仅含基础类型，marshal 不会失败；兜底返回空对象
		return "{}"
	}
	return string(b)
}

// ParsePublishTarget 解析 publish_target 列内容，兼容三种历史格式：
//   - 空串 → 零值
//   - "draft:{id}"（旧草稿格式）→ DraftID
//   - 裸 dyn_id（旧发布格式）→ DynID
//   - JSON（新格式）→ 结构化解析；JSON 解析失败时降级为裸 dyn_id
func ParsePublishTarget(s string) PublishTarget {
	s = strings.TrimSpace(s)
	if s == "" {
		return PublishTarget{}
	}
	if strings.HasPrefix(s, "{") {
		var t PublishTarget
		if err := json.Unmarshal([]byte(s), &t); err == nil {
			return t
		}
	}
	if strings.HasPrefix(s, "draft:") {
		return PublishTarget{DraftID: strings.TrimPrefix(s, "draft:")}
	}
	return PublishTarget{DynID: s}
}
