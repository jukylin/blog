## 声明
&nbsp;&nbsp;&nbsp;&nbsp;阅读本编文章需要go语言基础和对资源池有一些了解。

&nbsp;&nbsp;&nbsp;&nbsp;go 版本为1.11，[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)为2018-11-23的最新master版本

## 前言
&nbsp;&nbsp;&nbsp;&nbsp;在开始前我们先来简单定义一下协程池：```能够达到协程资源复用```。在这个定义下协程池的实现可以说是“百花齐放”了，找一下热门的go语言开源项目都会有协程池的不同实现方式。 有基于链表实现的[Tidb](https://github.com/pingcap/tidb/blob/v1.0.9/util/goroutine_pool/gp.go)，有基于环形队列实现的[Jaeger](https://github.com/jaegertracing/jaeger/blob/master/pkg/queue/bounded_queue.go)，有基于数组栈实现的[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)等，种类繁多任君选择。这么多的协程池实现可以归纳成二种：

 1. 提前创建协程：[Jaeger](https://github.com/jaegertracing/jaeger/blob/master/pkg/queue/bounded_queue.go)，[Istio](https://github.com/istio/istio/blob/master/mixer/pkg/pool/goroutine.go)，[Tars](https://github.com/TarsCloud/TarsGo)等。
 2. 按需创建协程：[Tidb](https://github.com/pingcap/tidb/blob/v1.0.9/util/goroutine_pool/gp.go)，[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)，[Ants](https://github.com/panjf2000/ants)等。

&nbsp;&nbsp;&nbsp;&nbsp;这2种实现中，个人比较喜欢第二种按需创建，[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)也是使用第二种方式，所以我们来看看它是如何实现的。

## FastHTTP协程池简介
&nbsp;&nbsp;&nbsp;&nbsp;在介绍[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)协程池之前先做一下简单的介绍。[workerChan](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L42)和协程一一对应，相同的生命周期，可以把[workerChan](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L42)看成是协程的门牌，使用凭证，引路子等。 整个协程池的实现主要由[workerPool](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L16)和[workerChan](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L42)组成。
 1. 请求进来创建协程
 2. 请求处理完成，把协程的[workerChan](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L42)放入[workerPool.ready](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L31)
 3. 再有请求进来，从[workerPool.ready](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L31)获取[workerChan](https://github.com/valyala/fasthttp/blob/master/workerpool.go#L42)，处理请求。
 4. 从第2步开始不断重复

## 协程池用在哪里
* go官方原生 ``` http.Server```

```
net/http/server.go #2805
func (srv *Server) Serve(l net.Listener) error {
    ......
    for {
      rw, e := l.Accept()
      ......
      //FastHTTP在这步使用协程池
      go c.serve(ctx)
    }
}
```

* FastHTTP的 ``` fasthttp.ListenAndServe ```

```
github.com/valyala/fasthttp/server.go 1489
func (s *Server) Serve(ln net.Listener) error {
    ......
    for {
      if c, err = acceptConn(s, ln, &lastPerIPErrorTime); err != nil {
          ......
      }
      //对应go原生的 go c.serve(ctx)
      if !wp.Serve(c) {
          ......
      }
      ......
    }
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;在go原生的```http.Server```包中，当接收到新请求就会启动一个协程处理，而FastHTTP则使用协程池处理。

## 获取workerChan
```
github.com/valyala/fasthttp/workerpool.go #156
func (wp *workerPool) getCh() *workerChan {
  var ch *workerChan
  createWorker := false

  wp.lock.Lock()
  ready := wp.ready
  n := len(ready) - 1
  if n < 0 {
    if wp.workersCount < wp.MaxWorkersCount {
      createWorker = true
      wp.workersCount++
    }
  } else {
    //从尾部获取Ch
    ch = ready[n]
    ready[n] = nil
    wp.ready = ready[:n]
  }
  wp.lock.Unlock()

  if ch == nil {
    //如果协程数超过上限，直接抛弃当前请求
    if !createWorker {
      return nil
    }
    vch := wp.workerChanPool.Get()
    if vch == nil {
      vch = &workerChan{
        ch: make(chan chan struct{}, workerChanCap),
      }
    }
    ch = vch.(*workerChan)
    //ch和协程绑定
    go func() {
      wp.workerFunc(ch)
      wp.workerChanPool.Put(vch)
    }()
  }
  return ch
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;在go语言中不同协程之间的通讯使用 ``` channel ```，在协程池中也不例外，FastHTTP创建了一个协程，就会和一个```workerChan```绑定，使用方根据这个 ``` workerChan ``` 就可以使用协程池里的资源。从上面的代码可以看出，使用协程池的资源，都是先从Slice的尾部弹出``` workerChan ```，在把``` workerChan ```交给使用放，如果Slice没有``` workerChan ```就会创建。


## 把workerChan放入Slice尾部
```
github.com/valyala/fasthttp/workerpool.go #194
func (wp *workerPool) release(ch *workerChan) bool {
  //用户清理
  ch.lastUseTime = time.Now()
  wp.lock.Lock()
  if wp.mustStop {
    wp.lock.Unlock()
    return false
  }
  //往尾部追加
  wp.ready = append(wp.ready, ch)
  wp.lock.Unlock()
  return true
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;当协程完成工作后，就会把```workerChan```放回Slice尾部，以待其他请求使用。

## 定期清理过期```workerChan```
```
github.com/valyala/fasthttp/workerpool.go #98
func (wp *workerPool) clean(scratch *[]*workerChan) {
    ......
  currentTime := time.Now()

  wp.lock.Lock()
  ready := wp.ready
  n := len(ready)
  i := 0
  for i < n && currentTime.Sub(ready[i].lastUseTime) > maxIdleWorkerDuration {
      i++
  }
  *scratch = append((*scratch)[:0], ready[:i]...)
  if i > 0 {
    m := copy(ready, ready[i:])
    for i = m; i < n; i++ {
      ready[i] = nil
    }
    wp.ready = ready[:m]
  }
  wp.lock.Unlock()

  ......
	tmp := *scratch
  for i, ch := range tmp {
      //让协程停止工作
    ch.ch <- nil
    tmp[i] = nil
  }
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;定期清理是为了避免在常态下空闲的协程过多，加重了调度层的负担。使用按需创建协程池的方式存在这样一个问题，高峰期的时候创建了很多协程，高峰期过后很多协程处于空闲状态，这就造成了不必要的开销。所以需要一种过期机制。在这里数组栈(FILO)的优点也体现出来了，因为栈的特点不活跃的```workerChan```都放在了数组的头部，所以只需要从数组头部开始轮询，一直到找到未过期的```workerChan```，再把这部分清理掉，就达到清理的效果，并且不需要轮询整个数组。


## 收益有多少
> &nbsp;&nbsp;&nbsp;&nbsp;花了点时间对FastHTTP的协程池进行了压测[代码](https://github.com/jukylin/blog/blob/master/pool_test.go)。
```
apple:gopool apple$ go test -bench=. -test.benchmem
goos: darwin
goarch: amd64
pkg: study_go/gopool
BenchmarkNotPool-4        	      10	4937881320 ns/op	107818560 B/op	  401680 allocs/op
BenchmarkFastHttpPool-4   	      10	 380807481 ns/op	13444607 B/op	  169946 allocs/op
BenchmarkAntsPoll-4       	      10	 429482715 ns/op	20756724 B/op	  302093 allocs/op
PASS
ok  	study_go/gopool	72.891s
```
> 从上面的对比来看使用协程池的收益还不少。



## 结语
> &nbsp;&nbsp;&nbsp;&nbsp;[FastHTTP](https://github.com/valyala/fasthttp/blob/master/workerpool.go)协程池的实现方式是我所了解的几种实现中，性能是比较突出的，当然其他协程池的实现方式也很有学习参考价值，在这个过程中复习了链表，数组栈，环形队列的使用场景。收获颇多。
