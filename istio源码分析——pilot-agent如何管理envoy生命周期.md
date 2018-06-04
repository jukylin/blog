
![image](https://upload.cc/i1/2018/06/04/xeo39R.png)

## 声明

1. 分析源码为0.7.1版本
2. 环境为k8s
3. 由于没有C++ 基础，所以源码分析止步于 C++，但也学到很多东西


## pilot-agent 是什么？

>　当我们执行 ```kubectl apply -f <(~istioctl kube-inject -f sleep.yaml) ``` 的时候，k8s就会帮我们建立3个容器。

```
[root@izwz9cffi0prthtem44cp9z ~]# docker ps |grep sleep
8e0de7294922        istio/proxy                                                               
ccddc800b2a2        registry.cn-shenzhen.aliyuncs.com/jukylin/sleep                          
990868aa4a42        registry-vpc.cn-shenzhen.aliyuncs.com/acs/pause-amd64:3.0           
```
> 在这3个容器中，我们关注```istio/proxy```。这个容器运行着2个服务。```pilot-agent```就是接下来介绍的：如何管理envoy的生命周期。

```
[root@izwz9cffi0prthtem44cp9z ~]# docker exec -it 8e0de7294922 ps -ef
UID        PID  PPID  C STIME TTY          TIME CMD
1337         1     0  0 May09 ?        00:00:49 /usr/local/bin/pilot-agent proxy
1337       567     1  1 09:18 ?        00:04:42 /usr/local/bin/envoy -c /etc/ist
```
## 为什么要用pilot-agent？

> envoy不直接和k8s，Consul，Eureka等这些平台交互，所以需要其他服务与它们对接，管理配置，pilot-agent就是其中一个 **【控制面板】**。


## 启动envoy

#### 加载配置
> 在启动前 pilot-agent 会生成一个配置文件：/etc/istio/proxy/envoy-rev0.json：

```
istio.io/istio/pilot/pkg/proxy/envoy/v1/config.go #88
func BuildConfig(config meshconfig.ProxyConfig, pilotSAN []string) *Config {
    ......
    return out
}
```
> 文件的具体内容可以直接查看容器里面的文件

```
docker exec -it 8e0de7294922 cat /etc/istio/proxy/envoy-rev0.json
```
> 关于配置内容的含义可以看[官方的文档](https://www.envoyproxy.io/docs/envoy/latest/configuration/overview/v2_overview)


#### 启动参数

> 一个二进制文件启动总会需要一些参数，envoy也不例外。

```
istio.io/istio/pilot/pkg/proxy/envoy/v1/watcher.go #274
func (proxy envoy) args(fname string, epoch int) []string {
    ......
    return startupArgs
}
```

> envoy启动参数可以通过 ```docker logs 8e0de7294922``` 查看，下面是从终端截取envoy的参数。了解具体的参数含义[官网文档](https://www.envoyproxy.io/docs/envoy/latest/operations/cli)。

```
-c /etc/istio/proxy/envoy-rev0.json --restart-epoch 0
--drain-time-s 45 --parent-shutdown-time-s 60
--service-cluster sleep 
--service-node sidecar~172.00.00.000~sleep-55b5877479-rwcct.default~default.svc.cluster.local 
--max-obj-name-len 189 -l info --v2-config-only
```


#### 启动envoy

> pilot-agent 使用 ```exec.Command```启动envoy，并且会监听envoy的运行状态（如果envoy非正常退出，status 返回非nil，pilot-agent会有策略把envoy重新启动）。

> ```proxy.config.BinaryPath``` 为envoy二进制文件路径：/usr/local/bin/envoy。

> ```args``` 为上面介绍的envoy启动参数。

```
istio.io/istio/pilot/pkg/proxy/envoy/v1/watcher.go #353
func (proxy envoy) Run(config interface{}, epoch int, abort <-chan error) error {
    ......
    /* #nosec */
    cmd := exec.Command(proxy.config.BinaryPath, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Start(); err != nil {
      return err
    }
    ......
    done := make(chan error, 1)
    go func() {
      done <- cmd.Wait()
    }()

    select {
    case err := <-abort:
      ......
    case err := <-done:
      return err
    }
}
```


## 热更新envoy

> 在这里我们只讨论pilot-agent如何让envoy热更新，至于如何去触发这步会在后面的文章介绍。

#### envoy热更新策略

![image](https://upload.cc/i1/2018/06/04/RKy3Yn.png)

> 想详细了解envoy的热更新策略可以看官网博客[Envoy hot restart](https://blog.envoyproxy.io/envoy-hot-restart-1d16b14555b5)。

> 简单介绍下envoy热更新步骤：

1. 启动另外一个envoy2进程（Secondary process）
2. envoy2通知envoy1（Primary process）关闭其管理的端口，由envoy2接管
3. 通过UDS把envoy1可用的listen sockets拿过来
3. envoy2初始化成功，通知envoy1在一段时间内（```drain-time-s```）优雅关闭正在工作的请求
5. 到了时间（```parent-shutdown-time-s```），envoy2通知envoy1自行关闭
6. envoy2升级为envoy1

> 从上面的执行步骤来看，poilt-agent只负责启动另一个envoy进程，其他由envoy自行处理。

#### 什么时候进行热更新？

>  在poilt-agent启动的时候，会监听```/etc/certs/```目录下的文件，如果这个目录下的文件被修改或删除，poilt-agent就会通知envoy进行热更新。至于如何触发对这些文件进行修改和删除会在接下来的文章介绍。

```
istio.io/istio/pilot/pkg/proxy/envoy/v1/watcher.go #177
func watchCerts(ctx context.Context, certsDirs []string, watchFileEventsFn watchFileEventsFn,
	minDelay time.Duration, updateFunc func()) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warnf("failed to create a watcher for certificate files: %v", err)
		return
	}
	defer func() {
		if err := fw.Close(); err != nil {
			log.Warnf("closing watcher encounters an error %v", err)
		}
	}()

	// watch all directories
	for _, d := range certsDirs {
		if err := fw.Watch(d); err != nil {
			log.Warnf("watching %s encounters an error %v", d, err)
			return
		}
	}
	watchFileEventsFn(ctx, fw.Event, minDelay, updateFunc)
}
```

#### 热更新启动参数

```
-c /etc/istio/proxy/envoy-rev1.json --restart-epoch 1
--drain-time-s 45 --parent-shutdown-time-s 60
--service-cluster sleep --service-node
sidecar~172.00.00.000~sleep-898b65f84-pnsxr.default~default.svc.cluster.local 
--max-obj-name-len 189 -l info
--v2-config-only
```

> 热更新启动参数和第一次启动参数的不同的地方是 -c 和 --restart-epoch，其实-c 只是配置文件名不同，它们的内容是一样的。--restart-epoch 每次进行热更新的时候都会自增1，用于判断是进行热更新还是打开一个存在的envoy（这里的意思应该是第一次打开envoy）
[具体看官方描述](https://www.envoyproxy.io/docs/envoy/latest/operations/cli#cmdoption-restart-epoch)

```
istio.io/istio/pilot/pkg/proxy/agent.go #258
func (a *agent) reconcile() {
    ......
    // discover and increment the latest running epoch
    epoch := a.latestEpoch() + 1
    // buffer aborts to prevent blocking on failing proxy
    abortCh := make(chan error, MaxAborts)
    a.epochs[epoch] = a.desiredConfig
    a.abortCh[epoch] = abortCh
    a.currentConfig = a.desiredConfig
    go a.waitForExit(a.desiredConfig, epoch, abortCh)
}
```

#### 从终端截取触发热更新的日志

```
2018-04-24T13:59:35.513160Z	info	watchFileEvents: "/etc/certs//..2018_04_24_13_59_35.824521609": CREATE
2018-04-24T13:59:35.513228Z	info	watchFileEvents: "/etc/certs//..2018_04_24_13_59_35.824521609": MODIFY|ATTRIB
2018-04-24T13:59:35.513283Z	info	watchFileEvents: "/etc/certs//..data_tmp": RENAME
2018-04-24T13:59:35.513347Z	info	watchFileEvents: "/etc/certs//..data": CREATE
2018-04-24T13:59:35.513372Z	info	watchFileEvents: "/etc/certs//..2018_04_24_04_30_11.964751916": DELETE
```


## 抢救envoy

> envoy是一个服务，既然是服务都不可能保证100%的可用，如果envoy不幸运宕掉了，那么pilot-agent如何进行抢救，保证envoy高可用？

#### 获取退出状态

> 在上面提到pilot-agent启动envoy后，会监听envoy的退出状态，发现非正常退出状态，就会抢救envoy。

```
func (proxy envoy) Run(config interface{}, epoch int, abort <-chan error) error {
    ......
    // Set if the caller is monitoring envoy, for example in tests or if envoy runs in same
    // container with the app.
    if proxy.errChan != nil {
      // Caller passed a channel, will wait itself for termination
      go func() {
        proxy.errChan <- cmd.Wait()
      }()
      return nil
    }

    done := make(chan error, 1)
    go func() {
      done <- cmd.Wait()
    }()
    ......
}
```

#### 抢救envoy

> 使用 kill -9 可以模拟envoy非正常退出状态。当出现非正常退出，pilot-agent的抢救机制会被触发。如果第一次抢救成功，那当然是好，如果失败了，pilot-agent会继续抢救，最多抢救10次，每次间隔时间为 2 <sup>n</sup> * 100 * time.Millisecond。超过10次都没有救活，pilit-agent就会放弃抢救，宣布死亡，并且退出istio/proxy，让k8s重新启动一个新容器。

```
istio.io/istio/pilot/pkg/proxy/agent.go #164
func (a *agent) Run(ctx context.Context) {
  ......
  for {
    ......
    select {
        ......
    case status := <-a.statusCh:
        ......
      if status.err == errAbort {
        //pilot-agent通知退出 或 envoy非正常退出
        log.Infof("Epoch %d aborted", status.epoch)
      } else if status.err != nil {
        //envoy非正常退出
        log.Warnf("Epoch %d terminated with an error: %v", status.epoch, status.err)
                ......
        a.abortAll()
      } else {
        //正常退出
        log.Infof("Epoch %d exited normally", status.epoch)
      }
    ......
    if status.err != nil {
      // skip retrying twice by checking retry restart delay
      if a.retry.restart == nil {
        if a.retry.budget > 0 {
          delayDuration := a.retry.InitialInterval * (1 << uint(a.retry.MaxRetries-a.retry.budget))
          restart := time.Now().Add(delayDuration)
          a.retry.restart = &restart
          a.retry.budget = a.retry.budget - 1
          log.Infof("Epoch %d: set retry delay to %v, budget to %d", status.epoch, delayDuration, a.retry.budget)
        } else {
          //宣布死亡，退出istio/proxy
          log.Error("Permanent error: budget exhausted trying to fulfill the desired configuration")
          a.proxy.Panic(a.desiredConfig)
          return
        }
      } else {
        log.Debugf("Epoch %d: restart already scheduled", status.epoch)
      }
    }
    case <-time.After(delay):
        ......
    case _, more := <-ctx.Done():
        ......
    }
  }
}
```


```
istio.io/istio/pilot/pkg/proxy/agent.go #72
var (
  errAbort = errors.New("epoch aborted")
  // DefaultRetry configuration for proxies
  DefaultRetry = Retry{
    MaxRetries:      10,
    InitialInterval: 200 * time.Millisecond,
  }
)
```


#### 抢救日志

```
Epoch 6: set retry delay to 200ms, budget to 9
Epoch 6: set retry delay to 400ms, budget to 8
Epoch 6: set retry delay to 800ms, budget to 7
```

## 优雅关闭envoy

![image](https://upload.cc/i1/2018/06/04/MceDXR.png)

> 服务下线或升级我们都希望它们能很平缓的进行，让用户无感知 ，避免打扰用户。这就要服务收到退出通知后，处理完正在执行的任务才关闭，而不是直接关闭。envoy是否支持优雅关闭？这需要k8s，pilot-agent也支持这种玩法。因为这存在一种关联关系k8s管理pilot-agent，pilot-agent管理envoy。

#### k8s让服务优雅退出

>  网上有篇博客总结了[k8s优雅关闭pods](https://pracucci.com/graceful-shutdown-of-kubernetes-pods.html)，我这边简单介绍下优雅关闭流程：

1. k8s 发送 **SIGTERM** 信号到pods下所有服务的1号进程
2. 服务接收到信号后，优雅关闭任务，并退出
3. 过了一段时间（default 30s）,如果服务没有退出，k8s会发送 **SIGKILL** 信号，让容器强制退出。

#### pilot-agent 让envoy优雅退出

* pilot-agent接收k8s信号
> pilot-agent会接收syscall.SIGINT, syscall.SIGTERM，这2个信号都可以达到优雅关闭envoy的效果。

```
istio.io/istio/pkg/cmd/cmd.go #29
func WaitSignal(stop chan struct{}) {
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    <-sigs
    close(stop)
    _ = log.Sync()
}
```

* 通知子服务关闭envoy

> 在golang有一个上下文管理包 ```context```，这个包通过广播的方式通知各子服务执行关闭操作。

```
istio.io/istio/pilot/cmd/pilot-agent/main.go #242
ctx, cancel := context.WithCancel(context.Background())
go watcher.Run(ctx)
stop := make(chan struct{})
cmd.WaitSignal(stop)
<-stop
//通知子服务
cancel()

istio.io/istio/pilot/pkg/proxy/agent.go
func (a *agent) Run(ctx context.Context) {
  ......
  for {
    ......
    select {
    ......
    //接收到主服务信息通知envoy退出
    case _, more := <-ctx.Done():
      if !more {
        a.terminate()
        return
      }
    }
  }
}

istio.io/istio/pilot/pkg/proxy/envoy/v1/watcher.go #297
func (proxy envoy) Run(config interface{}, epoch int, abort <-chan error) error {
    ......
    select {
    case err := <-abort:
      log.Warnf("Aborting epoch %d", epoch)
      //发送 KILL信号给envoy
      if errKill := cmd.Process.Kill(); errKill != nil {
        log.Warnf("killing epoch %d caused an error %v", epoch, errKill)
      }
      return err
      ......
    }
}
```
> 上面展示了pilot-agent从k8s接收信号到通知envoy关闭的过程，这个过程说明了poilt-agent也是支持优雅关闭。但最终envoy并不能进行优雅关闭，这和pilot-agent发送KILL信号没关系，这是因为envoy本身就不支持。

#### envoy优雅关闭

* 遗憾通知

> 来到这里很遗憾通知你envoy自己不能进行优雅关闭，envoy会接收SIGTERM，SIGHUP，SIGCHLD，SIGUSR1这4个信号，但是这4个都与优雅无关，这4个信号的作用可看[官方文档](https://www.envoyproxy.io/docs/envoy/latest/operations/hot_restarter)。当然官方也注意到这个问题，可以到github了解一下[2920](https://github.com/envoyproxy/envoy/issues/2920) [3307](https://github.com/envoyproxy/envoy/pull/3307)。

* 替代方案

> 其实使用优雅关闭想达到的目的是：让服务平滑升级，减少对用户的影响。所以我们可以用[金丝雀部署](https://kubernetes.feisky.xyz/zh/apps/istio-traffic-management.html#%E9%87%91%E4%B8%9D%E9%9B%80%E9%83%A8%E7%BD%B2)来实现，并非一定要envoy实现。大致的流程：

1. 定义服务的旧版本（v1），新版本（v2）
2. 发布新版本
3. 将流量按照梯度的方式，慢慢迁移到v2
4. 迁移完成，运行一段时间，没问题就关闭v1

* golang 优雅退出HTTP服务

> 借此机会了解下golang的优雅关闭，golang在1.8版本的时候就支持这个特性

```
net/http/server.go #2487
func (srv *Server) Shutdown(ctx context.Context) error {
  atomic.AddInt32(&srv.inShutdown, 1)
  defer atomic.AddInt32(&srv.inShutdown, -1)

  srv.mu.Lock()
  // 把监听者关掉
  lnerr := srv.closeListenersLocked()
  srv.closeDoneChanLocked()
	//执行开发定义的函数如果有
  for _, f := range srv.onShutdown {
    go f()
  }
	srv.mu.Unlock()

  //定时查询是否有未关闭的链接
  ticker := time.NewTicker(shutdownPollInterval)
  defer ticker.Stop()
  for {
    if srv.closeIdleConns() {
      return lnerr
    }
    select {
    case <-ctx.Done():
      return ctx.Err()
    case <-ticker.C:
    }
  }
}
```

> 其实golang的关闭机制和envoy在github上讨论优雅关闭机制很相似：

###### golang机制
1. 关闭监听者（```ln, err := net.Listen("tcp", addr)```，向ln赋nil）
2. 定时查询是否有未关闭的链接
3. 所有链接都是退出，服务退出

###### envoy机制：
1. ingress listeners stop accepting new connections (clients see TCP connection refused) but continues to service existing connections. egress listeners are completely unaffected
2. configurable delay to allow workload to finish servicing existing connections
3. envoy (and workload) both terminate
