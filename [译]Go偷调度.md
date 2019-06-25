原文:[Go's work-stealing scheduler](https://rakyll.org/scheduler/)

Go调度器的工作是把可运行的协程分发到多个工作线程，这样协程就可以运行在一个或多个处理器。在多线程计算中，有2种调度模式：分享工作（work sharing） 和 偷工作（work stealing）。

- 分享工作：当处理器创建了一些新线程，它会企图迁移其中一些线程到其他处理器并希望空闲的处理器能有效利用它们。
- 偷工作：未被充分利用的处理器主动查找其他处理器的线程并把它们“偷”过来

偷工作没有分享工作那么频繁的迁移协程。当所有处理器有工作执行，就不会有线程被迁移。当有一个空闲的处理器，才考虑迁移。

Go到了1.1才有实现了偷调度，由Dmitry Vyukov贡献。这篇文章深入解释什么是偷调度和Go如何实现。

## 调度原理
Go是一个M:N模型调度器，这样可以有效利用多个处理器。在任何时候，M个协程需要被N个线程调度并运行在最多GOMAXPROCS个处理器。下面是Go调度器的术语：

- G: goroutine
- M: OS thread (machine)
- P: processor

每个P有一个本地协程队列，全局也有一个队列。每个M将会指派给一个P。P可能没有M，如果它处于阻塞或系统调用。任何时候，最多有GOMAXPROCS个P。任何时候，仅有一个M运行在一个P上。如果需要，调度器可以创建多个M。
![scheduler-concepts](https://upload.cc/i1/2019/06/24/zbZsIW.png)

每一轮的调度周期只是找到一个可运行的协程并执行它。每一轮调度周期，按下面规则进行搜索：
```
runtime.schedule() {
    // only 1/61 of the time, check the global runnable queue for a G.
    // if not found, check the local queue.
    // if not found,
    //     try to steal from other Ps.
    //     if not, check the global runnable queue.
    //     if not found, poll network.
}
```
一旦找到一个可运行的G，就会执行它，直到它被阻塞为止。

## 偷
当创建一个G或存在的G变成可运行状态，它会被推入到当前P可运行协程列表。当P执行完G，它会尝试从自己的可运行列表弹出一个G。如果列表现在是空的，P随机选择其他P并尝试从它的队列偷一半可运行的协程。
![scheduler-stealing](https://upload.cc/i1/2019/06/25/SOFxpd.png)

在上面的例子，P2没有找到可运行协程。因此，它随机选择另一个处理器（P1）并从中偷取3个协程到自己的本地队列。P2将会运行这些协程，调度器的工作在多个处理器之间将会更加公平分布。
## 线程自旋
调度器为了有效利用处理器总是希望尽可能多的分发可运行的协程到M上，但同时为了节省CPU资源，我们需要停止过多工作。矛盾的是, 调度器还需要能扩展支持高吞吐量和CPU密集程序。
如果性能很重要，不断抢占不但昂贵而且对于高吞吐量程序也是一个问题。线程不应该频繁的切换可运行的协程，因为它会导致延迟增加。在系统调用场景下，线程需要不断的在阻塞和非阻塞切换。成本贵且增加了大量开销。
为了最小化切换，Go调度器实现了“线程自旋”。线程自旋消耗少量的CPU资源，但它们最小化了线程的抢占。下面是线程自旋的情景：
- 分配了P的M找可运行协程的时候。
- 没有分配P的M找可用P的时候。
- Scheduler also unparks an additional thread and spins it when it is readying a goroutine if there is an idle P and there are no other spinning threads

任何时候，最多有GOMAXPROCS个自旋M。当自旋的线程找到工作，它会使自己脱离自旋状态。
如果空闲M没有分配P，则分配了P的空闲线程不会阻塞。当创建了一个新协程或M被阻塞时，调度程序确保至少有一个自旋的M。这样可以确保没有可运行的协程以其他方式运行；并且避免了过多的M在阻塞和非阻塞之间切换。
## 结语
Go调度器做了大量工作避免过度抢占系统线程，通过偷的工作方式调度它们到正确和未被完全利用的处理器上，以及实现“自旋”线程避免高频率的阻塞和非阻塞切换。
调度事件可以通过[execution tracer](https://golang.org/cmd/trace/)追踪。如果你认为你的处理器利用率很差，你可以调查一下发生了什么事情。
