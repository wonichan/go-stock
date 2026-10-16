// Package webbridge 把绑定给 Wails 的 *App 方法以 HTTP/WebSocket 形式暴露给浏览器，
// 并把后端 EventsEmit 事件推送到浏览器，供 -tags server 的 Web 服务构建使用。
//
// 调用语义与 Wails v2 绑定保持一致：参数按位置以 JSON 传入并反序列化为形参类型；
// 返回值为 (T) / (T, error) / (error) / ()，error 非空时调用失败并返回其消息。
package webbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sort"

	"go-stock/backend/logger"
)

var (
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
)

type method struct {
	name   string
	fn     reflect.Value
	in     []reflect.Type
	hasErr bool // 最后一个返回值为 error
	nOut   int
}

// Dispatcher 通过反射调用目标对象（*App）的导出方法。
type Dispatcher struct {
	ctx     context.Context
	methods map[string]*method
	deny    map[string]string
}

// NewDispatcher 收集 target 的全部导出方法（与 Wails Bind 暴露的方法集一致）。
// deny 为「方法名 → 拒绝原因」，用于在 Web 模式下屏蔽不适用的桌面方法。
func NewDispatcher(ctx context.Context, target any, deny map[string]string) *Dispatcher {
	d := &Dispatcher{ctx: ctx, methods: map[string]*method{}, deny: deny}
	v := reflect.ValueOf(target)
	t := v.Type()
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		fn := v.Method(i)
		ft := fn.Type()
		mi := &method{name: m.Name, fn: fn, nOut: ft.NumOut()}
		for j := 0; j < ft.NumIn(); j++ {
			mi.in = append(mi.in, ft.In(j))
		}
		if mi.nOut > 0 && ft.Out(mi.nOut-1) == errorType {
			mi.hasErr = true
		}
		d.methods[m.Name] = mi
	}
	return d
}

// Methods 返回可调用方法名（已排序，不含被屏蔽的方法）。
func (d *Dispatcher) Methods() []string {
	out := make([]string, 0, len(d.methods))
	for n := range d.methods {
		if _, denied := d.deny[n]; !denied {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Call 调用方法；result 已是 JSON 编码后的返回值。
func (d *Dispatcher) Call(name string, args []json.RawMessage) (result json.RawMessage, err error) {
	if reason, denied := d.deny[name]; denied {
		return nil, fmt.Errorf("%s: Web 模式下不可用（%s）", name, reason)
	}
	m, ok := d.methods[name]
	if !ok {
		return nil, fmt.Errorf("未知方法: %s", name)
	}
	in := make([]reflect.Value, len(m.in))
	ai := 0
	for i, pt := range m.in {
		if pt == contextType {
			in[i] = reflect.ValueOf(d.ctx)
			continue
		}
		pv := reflect.New(pt)
		if ai < len(args) && len(args[ai]) > 0 && string(args[ai]) != "null" {
			if err := json.Unmarshal(args[ai], pv.Interface()); err != nil {
				return nil, fmt.Errorf("%s: 第 %d 个参数解析失败: %v", name, ai+1, err)
			}
		}
		ai++
		in[i] = pv.Elem()
	}

	defer func() {
		if r := recover(); r != nil {
			logger.SugaredLogger.Errorf("webbridge: %s panic: %v\n%s", name, r, debug.Stack())
			result, err = nil, fmt.Errorf("%s 执行异常: %v", name, r)
		}
	}()
	outs := m.fn.Call(in)
	if m.hasErr {
		if e := outs[m.nOut-1]; !e.IsNil() {
			return nil, e.Interface().(error)
		}
		outs = outs[:m.nOut-1]
	}
	switch len(outs) {
	case 0:
		return json.RawMessage("null"), nil
	case 1:
		b, err := json.Marshal(outs[0].Interface())
		if err != nil {
			return nil, fmt.Errorf("%s: 返回值序列化失败: %v", name, err)
		}
		return b, nil
	default:
		return nil, errors.New(name + ": 不支持多个非 error 返回值")
	}
}
