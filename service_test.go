package GeeRPC

import (
	"fmt"
	"reflect"
	"testing"
)

type Foo int
type Args struct{ Num1, Num2 int }

// sum 非导出方法
func (f Foo) sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

// Sum 导出方法
func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

// _assert 断言,如果cond为false，则panic
func _assert(cond bool, msg string, v ...interface{}) {
	if !cond {
		panic(fmt.Sprintf("assert failed! "+msg, v...))
	}
}

// TestNewService 测试NewService
func TestNewService(t *testing.T) {
	var foo Foo
	s := newService(&foo) // 创建服务
	_assert(s != nil, "TestNewService failed!")
	_assert(len(s.method) == 1, "TestNewService failed!")
	mType := s.method["Sum"]
	_assert(mType != nil, "TestNewService failed!")
}

// TestCall 测试Call
func TestCall(t *testing.T) {
	var foo Foo
	s := newService(&foo)
	mType := s.method["Sum"]
	argv := mType.newArgv()
	replyv := mType.newReplyv()
	argv.Set(reflect.ValueOf(Args{Num1: 1, Num2: 2}))
	err := s.call(mType, argv, replyv)
	_assert(err == nil && *replyv.Interface().(*int) == 3, "TestCall failed!")
}
