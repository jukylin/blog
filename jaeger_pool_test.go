package pool_test

import (
	"testing"
	"sync"
)


type Person struct{
	Name string
	Age int
}

var personPool = sync.Pool{
	New: func() interface{} {
		return &Person{}
	},
}

var num = 10000000


func BenchmarkPointer(b *testing.B) {
	b.N = num
	personChan := make(chan *Person, num)

	for i := 0; i < b.N; i++ {
		p := &Person{}
		p.Age = i
		personChan <- p
	}
}


func BenchmarkPoolPointerNotPutCommon(b *testing.B) {
	b.N = num
	personChan := make(chan *Person, num)

	for i := 0; i < b.N; i++ {
		p := personPool.Get().(*Person)
		p.Age = i
		personChan <- p
	}
}

/**
10000000	       190 ns/op
10000000	       320 ns/op
 */
