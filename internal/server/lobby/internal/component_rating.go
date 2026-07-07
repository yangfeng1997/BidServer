// src/servers/lobbysvr/internal/component_rating.go
package internal

const (
	RatingComponentName = "rating"
	RatingField         = "rating"
	defaultMMR          = 1000
)

// RatingState 评分存储态（内嵌 players 文档 rating 子文档）
type RatingState struct {
	MMR int64 `bson:"mmr"`
}

func NewRatingState() RatingState { return RatingState{MMR: defaultMMR} }

// Rating 最小评分组件；本阶段只读（无 mutator，赛后改分留扩展点）。仅主循环用，零锁。
type Rating struct {
	mmr   int64
	dirty bool
}

func NewRating() *Rating { return &Rating{mmr: defaultMMR} }

func (r *Rating) Name() string  { return RatingComponentName }
func (r *Rating) Field() string { return RatingField }
func (r *Rating) Dirty() bool   { return r.dirty }
func (r *Rating) ClearDirty()   { r.dirty = false }
func (r *Rating) MarkDirty()    { r.dirty = true }

// Load 从存储态恢复；mmr 为 0（旧档缺字段）时回填默认 1000。
func (r *Rating) Load(s *RatingState) {
	r.mmr = s.MMR
	if r.mmr == 0 {
		r.mmr = defaultMMR
	}
	r.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (r *Rating) Snapshot() any { return RatingState{MMR: r.mmr} }

// MMR 返回当前评分（发起匹配时读取填入请求）
func (r *Rating) MMR() int64 { return r.mmr }

// 编译期断言 Rating 满足 Component
var _ Component = (*Rating)(nil)
