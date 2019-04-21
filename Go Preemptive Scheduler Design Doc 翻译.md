
原文：[Go Preemptive Scheduler Design Doc](https://docs.google.com/document/d/1ETuA2IOmnaQ4j81AtTGT40Y4_Jr6_IDASEKg0t0dBR8/edit#heading=h.3pilqarbrc9h)（需要翻墙）

## 声明
* 不是最终稿，还需要修整

## 问题

 1. 由于没有抢占调度，导致一些用户程序的行为明显不当，一个协程可以一直运行，没有切换到其他协程，定时器也没起作用。
 2. 当前GC需要“stop the world”，w/o 意味着抢占可以是任意时候

## 提出解决方案
这个方案是使用现有的split stack 机制进行抢占。每个协程在每次进行函数调用时都会进行 split stack 检查。如果超过栈堆的大小，*就会进入到runtime调用*。如果我们通过外部请求让这个检查失败，我们就可以进行抢占。
Sysmon 线程监听P里执行时间超过 X ms的协程，如果找到，则将它的 ``` g->stackguard ``` 设置一个很大的值，那么在下一次split stack 检查协程的时候调用 ``` morestack() ```。为了抢占```Morestack()```被修改去检查```g->stackguard```,有必要会调用调度器。
在```stop the world```期间，GC为所有当前运行的协成发出抢占请求。

这样可以实现完美的```precise GC```，因为协程抢占仅在可控点。这样我们可以为GC发送所有有需要的信息。

## Pros
* 没有增加额外开销
* 实现相对简单，没有信号，最小同步

## Cons
* 增加```runtime```的复杂性

## 评估
为了更好的评估我使用了设计模型
[https://codereview.appspot.com/9136045](https://codereview.appspot.com/9136045)

Go1 benchmark suite shows no slowdown:
```
benchmark                         old ns/op    new ns/op    delta
BenchmarkBinaryTree17            4604208550   4607734202   +0.08%
BenchmarkFannkuch11              2903981794   2925657766   +0.75%
BenchmarkFmtFprintfEmpty                 83           82   -1.44%
BenchmarkFmtFprintfString               210          210   +0.00%
BenchmarkFmtFprintfInt                  171          168   -1.75%
BenchmarkFmtFprintfIntInt               284          271   -4.58%
BenchmarkFmtFprintfPrefixedInt          259          260   +0.39%
BenchmarkFmtFprintfFloat                377          377   +0.00%
BenchmarkFmtManyArgs                   1067         1053   -1.31%
BenchmarkGobDecode                  8793949      8781962   -0.14%
BenchmarkGobEncode                 10055812     10144175   +0.88%
BenchmarkGzip                     422396880    413429581   -2.12%
BenchmarkGunzip                   100201142     99900778   -0.30%
BenchmarkHTTPClientServer             43658        43523   -0.31%
BenchmarkJSONEncode                34239405     33973689   -0.78%
BenchmarkJSONDecode                79118008     77890662   -1.55%
BenchmarkMandelbrot200              4033471      4034173   +0.02%
BenchmarkGoParse                    5209448      5256012   +0.89%
BenchmarkRegexpMatchEasy0_32            106          107   +0.94%
BenchmarkRegexpMatchEasy0_1K            301          300   -0.33%
BenchmarkRegexpMatchEasy1_32             89           90   +0.67%
BenchmarkRegexpMatchEasy1_1K            755          749   -0.79%
BenchmarkRegexpMatchMedium_32           163          163   +0.00%
BenchmarkRegexpMatchMedium_1K         59182        58977   -0.35%
BenchmarkRegexpMatchHard_32            2796         2810   +0.50%
BenchmarkRegexpMatchHard_1K           91888        92296   +0.44%
BenchmarkRevcomp                  685704524    687030150   +0.19%
BenchmarkTemplate                 111448907    111908050   +0.41%
BenchmarkTimeParse                      408          405   -0.74%
BenchmarkTimeFormat                     437          438   +0.23%
```

下面程序演示GC "stop the world" 的问题
[http://play.golang.org/p/Yzc4Vx-KaF](http://play.golang.org/p/Yzc4Vx-KaF)
```
Current GC trace:
gc7(8): 0+0+429 ms, 3462 -> 2908 MB
gc8(8): 0+0+296 ms, 5830 -> 3861 MB
gc9(8): 0+0+661 ms, 7758 -> 3825 MB
gc10(8): 0+0+939 ms, 7664 -> 4014 MB
gc11(8): 0+0+907 ms, 8063 -> 4016 MB

GC trace with the preemptive scheduler:
gc8(8): 0+0+126 ms, 4989 -> 3020 MB
gc9(8): 0+0+124 ms, 6057 -> 3249 MB
gc10(8): 0+0+72 ms, 6499 -> 3711 MB
gc11(8): 0+0+121 ms, 7434 -> 3250 MB
```

注意："stop the world" 停顿时间明显减少

下面的测试现在不起作用，但适用于抢占式调度程序：
[http://play.golang.org/p/86i_dRxWBm
](http://play.golang.org/p/86i_dRxWBm)

## 实现计划

 1. 引进一个G->StackGuard变量的副本，因为它可以被覆盖在抢占期间。
 2. Sysmon后台线程监听执行时间x ms的协成（初始化推荐10ms），将抢占标记存储到```g->stackguard```中。
 3. GC对所有运行的G都发出抢占请求。
 4. ```Morestack()```检查抢占标记并有可能改变协成的状态
 5. 使用```m->lock++/--``` 保护关键部分在运行时（例如runtime.newproc, runtime.ready），参见下面的非抢占区域部分
 *6. 用GC正确同步终结器Goroutine，因为它不再关联已知的抢占点。*
 7. 删除在```chan/hashmap/malloc```中存在的```runtime.gcwaiting```检查（低抢占权）
 
 在这点上我们获得了运行中的抢占调度

 8. 当前```framesize```并不再同步```morestack（）```来保存代码大小。在抢占后（甚至抢占失败），不可能再使用当前的```stack frame```，并且每次都会强制分配一个新的帧。建议的解决方案是引入```morestackNxM()```方法，其中N是 argsize (8,16..64)，M 是 framesize (8,16..64)， i.e. 64 functions; 通常 ```morestack()``` 函数会显式地接受 ```argsize``` and ```framesize``` 2个参数
 9. 重构```gogo/gogocall/gogocallfn```。有3个地方需要修改，还有一个抢占后恢复上下文（```gogo``` 恢复 DX -- 关闭上下文）。我们可以有一个上下文切换函数，它接收和恢复 AX 和 DX。建议接口：
```
// "Executes" PUSH PC in the BUF context.
void runtime·returnto(Gobuf *BUF, uintptr PC);

// Moves CRET into AX, CTX into DX and switches to BUF.
void runtime·gogogo(Gobuf *BUF, uintptr CRET, uintptr CTX);
```
现有函数和新函数```gogo2```可以根据接口实现如下：
```
void	runtime·gogo(Gobuf *buf, uintptr cret)
{
runtime·gogogo(buf, cret, 0);
}
void	runtime·gogocall(Gobuf *buf, void(*f)(void), uintptr ctx)
{
	runtime·returnto(buf, buf.pc);
	buf.pc = f;
runtime·gogogo(buf, 0, ctx);
}
void	runtime·gogocallfn(Gobuf *buf, FuncVal *fn)
{
	runtime·returnto(buf, buf.pc);
	buf.pc = *(uintptr*)fn;
runtime·gogogo(buf, 0, fn);
}
void	runtime·gogo2(Gobuf *buf, uintptr ctx)
{
runtime·gogogo(buf, 0, ctx);
}
```
1~9点已经在 Go1.2前实现。

 10. 收集调度器的体验，确定是否有必要在```back edges```编译额外的抢占检查。在函数入口检查在大多数情况下是足够了，因此不清楚是否有必要对```back edges``` 进行检查。检查可能是：
 ```
 MOV	[g], CX
CMP	$-1, g_stackguard(CX)
JNZ	nopreempt
CALL	$runtime.preempt(SB)
nopreempt:
 ```
 
 11. iant@提出一个优化建议，为```stackguard```分配一个额外的TLS位置。这将允许优化```split stack```检查和额外的抢占检查。
 ```
 CMP	$-1, [stackguard]
JNZ	nopreempt
CALL	$runtime.preempt(SB)  // can be further moved onto cold path
nopreempt:
 ```
 10, 11点还没在Go1.2实现

 
 ## 非抢占区域
 抢占式调度程序为运行库增加了新的复杂性————一个goroutine可以在多个点被抢占和取消调度。当前Go语言没有为这件事做好准备。
 缓解这种问题的其中一个措施是采用一种保守的抢占方式。就是说如果下面其中一个条件成立协程就不会被抢占：
 * 持有运行时锁
 * 在g0执行
 * 内存分配中或gc在进行的时候
 * 不是Grunning状态
 * 没有 P 或 P的状态不是Prunning
 
 涵盖了大多数不需要抢占的情况。但是，仍有一些个例。进过我鉴定在调度器有2个地方：```runtime.newproc()``` 和 ```runtime.ready()```，在这两种情况下，Goroutine都可以在局部变量中持有P；这太糟了，如果```stoptheworld```，就会导致死锁(P永远不会停止)。
Chans受到锁的保护，hashmap似乎是安全的。
通常方案是：在共享数据结构（e.g. chans, hashmaps, scheduler, memory allocator, etc）处于不一致状态时，抢占必须被禁用，这个不一致会破坏调度器或GC。
建议方案是：使用```m->lock++/--```手动禁用抢占。它已经用于在运行时禁用GC和抢占。
 
 
## 术语介绍
*  precise GC

[How does Go's precise GC work?](https://stackoverflow.com/questions/26422896/how-does-gos-precise-gc-work)

[Precise Garbage Collection in C](https://www2.cs.arizona.edu/~collberg/Teaching/553/2011/Resources/pankhuri-slides.pdf)

[Precise GC Stack Roots](https://docs.google.com/document/d/13v_u3UrN2pgUtPnH4y-qfmlXwEEryikFu0SQiwk35SA/pub)
