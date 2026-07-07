// Package jumphash 实现 Jump Consistent Hash（Lamping & Veach, 2014）：
// 无环、无虚拟节点、零额外内存、计算极快、分布均衡。
//
// 取舍：尾部增删（成员排序后变动的是最末元素）仅迁移 ~1/N 的 key；
// 非尾部成员变动会使其后成员桶号顺移、迁移较多 key。在线态纯内存、
// 重建廉价，可接受（见 spec §12）。
package jumphash

import (
	"hash/fnv"
	"sort"
)

// Jump 计算 key 落到 [0, numBuckets) 的桶号；numBuckets<=0 返回 -1。
func Jump(key uint64, numBuckets int) int32 {
	if numBuckets <= 0 {
		return -1
	}
	var b, j int64 = -1, 0
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int32(b)
}

// Pick 把 members 去重升序后，用 Jump Hash(fnv1a64(key)) 选一个成员。
// members 为空返回 ("", false)。排序保证调用方（不同 router 实例）对
// 同一 key 选出同一成员，与传入顺序无关。
func Pick(members []string, key string) (string, bool) {
	uniq := dedupSorted(members)
	n := len(uniq)
	if n == 0 {
		return "", false
	}
	b := Jump(hashKey(key), n)
	if b < 0 || int(b) >= n {
		return "", false
	}
	return uniq[b], true
}

func hashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

func dedupSorted(members []string) []string {
	if len(members) == 0 {
		return nil
	}
	cp := append([]string(nil), members...)
	sort.Strings(cp)
	out := cp[:1]
	for _, m := range cp[1:] {
		if m != out[len(out)-1] {
			out = append(out, m)
		}
	}
	return out
}
