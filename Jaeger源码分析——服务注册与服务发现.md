## 声明
> &emsp;Jaeger官方并没有明确说明其**服务注册和服务发现**的具体使用和介绍，这部分功能是在分析源码的时候，发现其原理与**服务注册和服务发现**类似，所以结合自己对服务注册和服务发现的认识，做一次总结，有错还请指点。


## TChannel服务注册和服务发现

![image](https://upload.cc/i/02xS4i.jpg)

> &emsp;Jaeger不借助第三方工具也能实现服务注册和服务发现，这部分功能由其依赖的RPC框架提供。

### 第三方注册——手动注册

```linux
go run cmd/agent/main.go --collector.host-port=192.168.0.10:14267,192.168.0.11:14267
```

> &emsp;在启动agent的时候，可配置多个collector静态地址，这部分地址会形成一张注册表。

### 注册表

- 注册表结构
```golang
github.com/uber/tchannel-go/peer.go #59
type PeerList struct {
    sync.RWMutex
    parent          *RootPeerList
    //以hostPort为下标组成注册表
    peersByHostPort map[string]*peerScore
    //负载均衡实现
    peerHeap        *peerHeap
    scoreCalculator ScoreCalculator
    lastSelected    uint64
}
```
- 健康检查

```golang
github.com/jaegertracing/jaeger/pkg/discovery/peerlistmgr/peer_list_mgr.go  #150
func (m *PeerListManager) ensureConnections() {
    peers := m.peers.Copy()
    minPeers := m.getMinPeers(peers)
    numConnected, notConnected := m.findConnected(peers)
    //有一定量的链接，就不进行健康检查
    if numConnected >= minPeers {
      return
    }
    ......
    for i := range notConnected {
      // swap current peer with random from the remaining positions
      r := i + m.rnd.Intn(len(notConnected)-i)
      notConnected[i], notConnected[r] = notConnected[r], notConnected[i]
      // try to connect to current peer (swapped)
      peer := notConnected[i]
      m.logger.Info("Trying to connect to peer", zap.String("host:port", peer.HostPort()))
      //用于控制超时
      ctx, cancel := context.WithTimeout(context.Background(), m.connCheckTimeout)
      conn, err := peer.GetConnection(ctx)
      cancel()
      if err != nil {
        m.logger.Error("Unable to connect", zap.String("host:port", peer.HostPort()), zap.Duration("connCheckTimeout", m.connCheckTimeout), zap.Error(err))
        continue
      }
      ......
    }
}

```
> &emsp;在注册表上的地址，TChannel都会进行健康检查，每秒进行一次，如果0.25秒没有连接上，视为服务不可用。如果连接成功则保留当前服务实例，供agent提交数据使用。

```golang
github.com/uber/tchannel-go/connection.go #228
func (ch *Channel) newOutboundConnection(timeout time.Duration, hostPort string, events connectionEvents) (*Connection, error) {
    conn, err := net.DialTimeout("tcp", hostPort, timeout)
    if err != nil {
      if ne, ok := err.(net.Error); ok && ne.Timeout() {
        ch.log.WithFields(LogField{"hostPort", hostPort}, LogField{"timeout", timeout}).Infof("Outbound net.Dial timed out")
        err = ErrTimeout
      }
      return nil, err
    }

    return ch.newConnection(conn, hostPort, connectionWaitingToSendInitReq, events), nil
}
```

### 客户端服务发现

- 软负载均衡

```golang
github.com/uber/tchannel-go/peer.go #149
func (l *PeerList) choosePeer(prevSelected map[string]struct{}, avoidHost bool) *Peer {
    var psPopList []*peerScore
    var ps *peerScore
    ......
    size := l.peerHeap.Len()
    for i := 0; i < size; i++ {
      //把peer从Heap头部弹出来
      popped := l.peerHeap.popPeer()
      if canChoosePeer(popped.HostPort()) {
          ps = popped
          break
      }
      psPopList = append(psPopList, popped)
    }
    //不符合的放入Heap尾部
    for _, p := range psPopList {
        heap.Push(l.peerHeap, p)
    }

    if ps == nil {
        return nil
    }
    //符合条件的打分，再放入Heap尾部
    l.peerHeap.pushPeer(ps)
    ps.chosenCount.Inc()
    return ps.Peer
}
```

> &emsp;当Agent需要提交数据的时候，会从TChannel的负载均衡获取peer(服务信息)，当有多个的时候，TChannel通过轮询方式，查询peer。实现方式：注册表把所有peer放入peerHeap，先把peer从头部弹出，再把peer放回尾部，从而实现轮询策略的负载均衡。

- 重试

```golang
github.com/uber/tchannel-go/retry.go #212
func (ch *Channel) RunWithRetry(runCtx context.Context, f RetriableFunc) error {
    var err error

    opts := getRetryOptions(runCtx)
    rs := ch.getRequestState(opts)
    defer requestStatePool.Put(rs)
    //默认重试5次
    for i := 0; i < opts.MaxAttempts; i++ {
        rs.Attempt++

        if opts.TimeoutPerAttempt == 0 {
            err = f(runCtx, rs)
        } else {
            attemptCtx, cancel := context.WithTimeout(runCtx, opts.TimeoutPerAttempt)
            err = f(attemptCtx, rs)
            cancel()
        }

        if err == nil {
            return nil
        }
        if !opts.RetryOn.CanRetry(err) {
            if ch.log.Enabled(LogLevelInfo) {
                ch.log.WithFields(ErrField(err)).Info("Failed after non-retriable error.")
            }
          return err
        }
        ......
    }

    // Too many retries, return the last error
    return err
}
```

> &emsp;网络之间的通讯避免不了网络异常，所以为了提高可用性，重试是其中一种方式。当从负载均衡获取peer提交数据到Collector，如果提交失败，会再从负载均衡获取peer，最多5次，如果5次都不成功就会放弃这次提交。


## Consul+docker 服务注册和服务发现

![image](https://upload.cc/i/9Ib4Jp.jpg)

> &emsp;使用consul实现服务注册和服务发现是一件很简单的事情。很多功能都是开箱即用。


### 准备工作

- 启动Consul——ip：172.18.0.2
```golang
docker run -itd --network=backend \
-p 8400:8400 -p 8500:8500 -p 8600:53/udp \
-h node1 progrium/consul -server -bootstrap -ui-dir /ui
```

- 启动Agent

```golang
docker run \
-itd --network=backend \
--name=jaeger-agent \
-p5775:5775/udp \
-p6831:6831/udp \
-p6832:6832/udp \
-p5778:5778/tcp \
--dns-search="service.consul" --dns=172.18.0.2 \
jaegertracing/jaeger-agent \
/go/bin/agent-linux --collector.host-port=jaeger-collector:14267
```


- 启动Collector

```golang
#node1
docker run -itd --network=backend \
--name=jaeger-collector-node1 \
-p :14267 \
--dns-search="service.consul" --dns=172.18.0.2 \
jaegertracing/jaeger-collector \
/go/bin/collector-linux \
--span-storage.type=cassandra \
--cassandra.keyspace=jaeger_v1_dc \
--cassandra.servers=cassandra:9042

#node2
docker run -itd --network=backend \
--name=jaeger-collector-node2 \
-p :14267 \
--dns-search="service.consul" --dns=172.18.0.2 \
jaegertracing/jaeger-collector \
/go/bin/collector-linux \
--span-storage.type=cassandra \
--cassandra.keyspace=jaeger_v1_dc \
--cassandra.servers=cassandra:9042
```

### 服务注册——自动注册
```golang
docker run -itd --net=backend --name=registrator \
--volume=/var/run/docker.sock:/tmp/docker.sock \
gliderlabs/registrator:latest \
consul://172.18.0.2:8500
```

> &emsp;使用consul+docker的形式，只要部署好服务，就会被自动注册到consul，十分简单。

### 注册表

- 查看注册表信息
> 查看注册表信息http://localhost:8500/ui/#/dc1/nodes/node1

![image](http://chuantu.biz/t6/168/1512578405x-1404795766.jpg)

> &emsp;可以看到启动的2个Collector服务ip分别为：172.18.0.5和172.18.0.8

- [健康检查](https://www.consul.io/docs/agent/checks.html)

> &emsp;consul提供了很多种健康检查方式：HTTP、TCP、Docker、Shell和TTL。详情可以查看官网。


### 服务端服务发现

> &emsp;Consul相对于Agent和Collector是远程服务，所以提供了2种服务发现方式：HTTP和DNS，在这里主要使用是DNS，因为简单，轻量。

- DNS和软负载均衡

>  &emsp;当Agent通过DNS解析出多个IP的时候，Consul会**随机**选择一个IP给Agent实现负载均衡。

> &emsp;由于DNS存在缓存，所以有可能出现，服务不健康，一样会被正常解析，所以在默认情况下Consul是没有设置缓存时间，TTL为0，但是也考虑到了不缓存对Consul的压力，所以开放配置，让我们去决定缓存时间点[DNS Caching](https://www.consul.io/docs/guides/dns-cache.html)。

## 总结

> TChannel与Consul+docker实现的服务发现和服务注册中都有他们的优缺点：

### 服务注册

- TChannel

> &emsp;TChannel的服务注册适用于一些基础服务，例如Jaeger就属于一种基础服务，这种服务一旦部署很少会变动。

- Consul + docker

> &emsp;在现在docker流行的大环境下使用Consul实现的服务注册会简单很多，docker有一个特点就是ip地址是动态，所以它很适合业务场景，因为业务经常变动，服务也随着变化。


### 健康检查

> &emsp;TChannel和Consul都提供了健康检查，但是都只是检测服务是否正在运行，无法了解是否能够正常处理请求。

### 服务发现

- TChannel

> &emsp;TChannel使用的是客户端服务发现，这种方式相对于Consul的服务端服务发现的优点就是没有了远程网络开销，单点问题。同时缺点就是各个语言都需要自己实现注册表，负载均衡等功能。

- Consul

> &emsp;Consul使用服务端服务发现，它可以很好的和其他服务结合使用，不需要关心注册表，负载均衡等。而且关于网络开销和单点问题都提供了方案。
