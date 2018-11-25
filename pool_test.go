package gopool

import (
	//"io/ioutil"
	//"net"
	"testing"
	//"github.com/valyala/fasthttp/fasthttputil"
	"github.com/panjf2000/ants"
	"time"
	"sync"
	//"github.com/pingcap/tidb/util/goroutine_pool"
	//"github.com/tarscloud/tarsgo/tars/util/gpool"
)


var sleepTime = 50

var Parallelism = 100

var runTimes  = 10

var forTimes  = 200000

func morestack() {
	//var stack [200 * 1024]byte
	//if true {
	//	for i := 0; i < len(stack); i++ {
	//		stack[i] = 'a'
	//	}
	//}
	time.Sleep(time.Duration(sleepTime) * time.Millisecond)
}



func BenchmarkNotPool(b *testing.B) {
	b.N = runTimes
	b.ReportAllocs()
	b.ResetTimer()
	b.SetParallelism(Parallelism)

	var wg sync.WaitGroup

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wg.Add(forTimes)
			for j := 0; j < forTimes; j++ {
				go func() error {
					morestack()
					wg.Done()
					return nil
				}()}
			wg.Wait()
		}
	})

	b.StopTimer()
}



func BenchmarkFastHttpPool(b *testing.B) {
	b.N = runTimes
	b.ReportAllocs()
	b.ResetTimer()
	b.SetParallelism(Parallelism)

	stopCh := make(chan struct{}, 1)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {

			var wg sync.WaitGroup

			wp := &workerPool{
				WorkerFunc: func(arg chan struct{}) error {

					morestack()
					wg.Done()

					return nil
				},
				MaxWorkersCount: 2024 * 100,
				//connState:       func(net.Conn, ConnState) {},
				MaxIdleWorkerDuration: 2 * time.Second,
			}
			wp.Start()

			wg.Add(forTimes)
			for j := 0; j < forTimes; j++ {
				wp.Serve(stopCh)
			}
			wg.Wait()
			wp.Stop()
		}
	})
	b.StopTimer()
}


//func BenchmarkTidbPoll(b *testing.B)  {
//
//	b.N = runTimes
//	b.ReportAllocs()
//	b.ResetTimer()
//	b.SetParallelism(Parallelism)
//
//	b.RunParallel(func(pb *testing.PB) {
//		for pb.Next() {
//			tiDbPoll := gp.New(1 * time.Second)
//			var wg sync.WaitGroup
//
//			wg.Add(forTimes)
//			for j := 0; j < forTimes; j++ {
//				tiDbPoll.Go(func() {
//					morestack()
//					wg.Done()
//				})
//			}
//			wg.Wait()
//		}
//	})
//
//	b.StopTimer()
//}



func BenchmarkAntsPoll(b *testing.B)  {

	b.N = runTimes
	b.ReportAllocs()
	b.ResetTimer()
	b.SetParallelism(Parallelism)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var wg sync.WaitGroup
			p, _ := ants.NewPoolWithFunc(2024 * 100, func(arg interface{}) error {
				morestack()
				wg.Done()
				//stopCh <- struct{}{}
				return nil
			})

			wg.Add(forTimes)
			for j := 0; j < forTimes; j++ {
				p.Serve(1)
			}
			wg.Wait()

			p.Release()

			ants.Release()
		}
	})


	b.StopTimer()
}



const (
	poolSize  = 50000
	gpQueueSize = 5000
)

//func BenchmarkTarsPool(b *testing.B) {
//
//	b.N = runTimes
//	b.ReportAllocs()
//	b.ResetTimer()
//	b.SetParallelism(Parallelism)
//
//	b.RunParallel(func(pb *testing.PB) {
//		for pb.Next() {
//			var wg sync.WaitGroup
//
//			pool := gpool.NewPool(poolSize, gpQueueSize)
//
//			wg.Add(forTimes)
//			for j := 0; j < forTimes; j++ {
//				pool.JobQueue <- func() {
//					morestack()
//					wg.Done()
//				}
//			}
//			wg.Wait()
//			pool.Release()
//		}
//	})
//
//	b.StopTimer()
//}


