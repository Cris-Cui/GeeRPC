// Package service
// Description: 服务
// 服务的注意事项:
// 1. 方法必须是导出的
// 2. 方法必须有两个参数, 都是指针类型
// 3. 方法的第二个参数是指针类型, 并且返回值类型是error
// 4. 方法返回值只有error
package service

import (
	"log"
	"reflect"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// MethodType 方法类型
type MethodType struct {
	// method	方法本身
	method reflect.Method
	// ArgType	参数类型
	ArgType reflect.Type
	// ReplyType	返回值类型
	ReplyType reflect.Type
	// numCalls	调用次数
	numCalls uint64
}

// NumCalls 调用次数
func (m *MethodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls) // 原子操作, 读取调用次数
}

// newArgv 创建参数类型
func (m *MethodType) newArgv() reflect.Value {
	var argv reflect.Value
	// 如果参数不是指针类型, 则创建一个新的参数类型
	if m.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(m.ArgType.Elem()) // 创建指针类型
	} else {
		argv = reflect.New(m.ArgType).Elem() // 创建非指针类型
	}
	return argv
}

// newReplyv 创建返回值类型
func (m *MethodType) newReplyv() reflect.Value {
	// 创建返回值类型
	replyv := reflect.New(m.ReplyType.Elem())
	// 如果返回值不是指针类型, 则创建一个新的返回值类型
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem())) // 创建map类型
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0)) // 创建slice类型
	}
	return replyv
}

// service 服务类型
type service struct {
	// name	服务名称
	name string
	// typ  服务类型
	typ reflect.Type
	// rcvr 服务实例
	rcvr reflect.Value
	// method 服务方法
	method map[string]*MethodType
}

// NewService 创建服务
//
// rcvr		服务实例
// 返回值		服务
func NewService(rcvr interface{}) *service {
	s := new(service)                               // 创建服务
	s.rcvr = reflect.ValueOf(rcvr)                  // 获取服务实例
	s.name = reflect.Indirect(s.rcvr).Type().Name() // 获取服务名称
	s.typ = reflect.TypeOf(rcvr)                    // 获取服务类型
	if !isExported(s.name) {                        // 判断服务名称是否是导出的
		panic("rpc server: " + s.name + " is not a valid service name")
	}
	s.registerMethods() // 注册服务方法
	return s
}

// registerMethods 注册服务方法
func (s *service) registerMethods() {
	s.method = make(map[string]*MethodType) // 创建服务方法
	// 遍历服务类型的所有方法
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i) // 获取服务方法
		mType := method.Type      // 获取服务方法类型
		// 判断服务方法参数个数是否为3个, 且返回值个数是否为1个
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// 判断服务方法返回值类型是否为error
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		argType, replyType := mType.In(1), mType.In(2) // 获取服务方法参数类型和返回值类型
		// 判断服务方法参数类型是否为导出的, 且返回值类型是否为导出的
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		// 将服务方法注册到服务方法中
		s.method[method.Name] = &MethodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}

// call 调用服务方法
func (s *service) call(m *MethodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1) // 原子操作, 调用次数加1
	function := m.method.Func        // 获取服务方法
	// 调用服务方法
	returnValues := function.Call([]reflect.Value{s.rcvr, argv, replyv})
	// 判断服务方法返回值是否为error
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}

// isExported 判断名称是否是导出的
func isExported(name string) bool {
	// 判断名称是否是导出的
	rune, _ := utf8.DecodeRuneInString(name) // 获取名称的第一个字符
	return unicode.IsUpper(rune)             // 判断名称的第一个字符是否是大写
}

// isExportedOrBuiltinType 判断类型是否是导出的或内置的
func isExportedOrBuiltinType(t reflect.Type) bool {
	// 判断类型是否是导出的或内置的
	for t.Kind() == reflect.Ptr {
		t = t.Elem() // 获取指针类型的元素类型
	}
	return isExported(t.Name()) || t.PkgPath() == ""
}
