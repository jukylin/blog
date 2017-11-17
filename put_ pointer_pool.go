package main

import (
	"sync"
	"fmt"
)

type Count struct{
	Num int
}

var countPool = sync.Pool{
	New: func() interface{} {
		return &Count{}
	},
}

func main() {
	num := 10
	countChan := make(chan *Count, num)

	for i := 0; i < num; i++{
		c:= countPool.Get().(*Count)
		c.Num = i
		countChan <- c
		countPool.Put(c)
	}

	for i := 0; i < num; i++{
		select {
		case p2 := <-countChan:
			fmt.Printf("p2 %+v\n", p2)
		}
	}
	
	/**
	result
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	p2 &{Num:9}
	 */
}
