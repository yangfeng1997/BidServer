package rpc

import "sync/atomic"

// 并行汇合两个异构调用
func Join2[A, B any](ctx Ctx,
	a func(func(*A, error)),
	b func(func(*B, error)),
	done func(*A, *B, error),
) {
	var ra *A
	var rb *B
	var errA, errB error
	var left atomic.Int32
	left.Store(2)

	finish := func() {
		if left.Add(-1) != 0 {
			return
		}
		if errA != nil {
			done(nil, nil, errA)
			return
		}
		if errB != nil {
			done(nil, nil, errB)
			return
		}
		done(ra, rb, nil)
	}

	a(func(v *A, err error) { ra, errA = v, err; finish() })
	b(func(v *B, err error) { rb, errB = v, err; finish() })
	_ = ctx
}

// 并行汇合三个异构调用
func Join3[A, B, C any](ctx Ctx,
	a func(func(*A, error)),
	b func(func(*B, error)),
	c func(func(*C, error)),
	done func(*A, *B, *C, error),
) {
	var ra *A
	var rb *B
	var rc *C
	var errA, errB, errC error
	var left atomic.Int32
	left.Store(3)

	finish := func() {
		if left.Add(-1) != 0 {
			return
		}
		if errA != nil {
			done(nil, nil, nil, errA)
			return
		}
		if errB != nil {
			done(nil, nil, nil, errB)
			return
		}
		if errC != nil {
			done(nil, nil, nil, errC)
			return
		}
		done(ra, rb, rc, nil)
	}

	a(func(v *A, err error) { ra, errA = v, err; finish() })
	b(func(v *B, err error) { rb, errB = v, err; finish() })
	c(func(v *C, err error) { rc, errC = v, err; finish() })
	_ = ctx
}

// 并行汇合四个异构调用
func Join4[A, B, C, D any](ctx Ctx,
	a func(func(*A, error)),
	b func(func(*B, error)),
	c func(func(*C, error)),
	d func(func(*D, error)),
	done func(*A, *B, *C, *D, error),
) {
	var ra *A
	var rb *B
	var rc *C
	var rd *D
	var errA, errB, errC, errD error
	var left atomic.Int32
	left.Store(4)

	finish := func() {
		if left.Add(-1) != 0 {
			return
		}
		if errA != nil {
			done(nil, nil, nil, nil, errA)
			return
		}
		if errB != nil {
			done(nil, nil, nil, nil, errB)
			return
		}
		if errC != nil {
			done(nil, nil, nil, nil, errC)
			return
		}
		if errD != nil {
			done(nil, nil, nil, nil, errD)
			return
		}
		done(ra, rb, rc, rd, nil)
	}

	a(func(v *A, err error) { ra, errA = v, err; finish() })
	b(func(v *B, err error) { rb, errB = v, err; finish() })
	c(func(v *C, err error) { rc, errC = v, err; finish() })
	d(func(v *D, err error) { rd, errD = v, err; finish() })
	_ = ctx
}

// 对多个同构目标执行同一函数
func Each[T, R any](ctx Ctx, items []T, step func(T, func(*R, error)), done func([]*R, error)) {
	if len(items) == 0 {
		done(nil, nil)
		return
	}
	var pending atomic.Int32
	pending.Store(int32(len(items)))
	results := make([]*R, len(items))
	errs := make(chan error, len(items))

	for i, item := range items {
		i := i
		it := item
		step(it, func(v *R, err error) {
			if err != nil {
				select {
				case errs <- err:
				default:
				}
			}
			results[i] = v
			if pending.Add(-1) != 0 {
				return
			}
			select {
			case e := <-errs:
				done(results, e)
			default:
				done(results, nil)
			}
		})
	}
	_ = ctx
}

// 序列下一步触发器
type Next func(error)

// 顺序编排
type Sequence struct {
	ctx   Ctx
	steps []func(Next)
}

// 创建顺序编排器
func Seq(ctx Ctx) *Sequence { return &Sequence{ctx: ctx} }

// 追加一步
func (s *Sequence) Step(fn func(Next)) *Sequence {
	s.steps = append(s.steps, fn)
	return s
}

// 执行整个序列
func (s *Sequence) Done(done func(error)) {
	var run func(int)
	run = func(i int) {
		if i >= len(s.steps) {
			done(nil)
			return
		}
		s.steps[i](func(err error) {
			if err != nil {
				done(err)
				return
			}
			run(i + 1)
		})
	}
	run(0)
	_ = s.ctx
}
