
![image](https://upload.cc/i1/2018/06/22/g6TwvS.png)

## 声明

1. 这篇文章需要了解istio，k8s，golang，envoy基础知识
2. 分析的环境为k8s，istio版本为0.8.0

## pilot-discovery的作用

> envoy提供一套通用的数据面接口，通过接口可以动态实现服务发现和配置。在istio中需要集成k8s，consul等服务发现系统，所以需要一个中介整理在k8s，consul服务注册和配置信息，并提供给envoy。

## envoy v1 API 和 v2 API区别

> v1版本API和v2版本API有一段历史，详情可看[官网博客](https://blog.envoyproxy.io/the-universal-data-plane-api-d15cec7a)。在envoy开源之初，使用HTTP+轮询的方式实现动态服务发现和配置，但是这种方式存在以下缺点：
 1. 由于接口数据使用弱类型，导致实现一些通用服务比较困难。
 2. 控制面更喜欢使用推送的方式，来减少数据在更新时传输的时间。
> 随着和Google合作加强，官方使用GRPC + push开发了v2版本API，实现了v1版本的SDS/CDS/RDS/LDS接口，继续支持```JSON/YAML```数据格式，还增加了ADS（把SDS/CDS/RDS/LDS4个接口合在一下），HDS等接口。

## 建立基础缓存数据
> 其实pilot-discovery已经算是一个小型的非持久性key/value数据库了，它把istio的配置信息和服务注册信息都进行了缓存。这样可以使配置更快的生效。 

#### 缓存了什么数据
 
 - istio配置
```
istio.io/istio/pilot/pkg/model/config.go
var (
    ......
    // RouteRule describes route rules
    RouteRule = ProtoSchema{
      Type:        "route-rule",
      ......
    }
    // VirtualService describes v1alpha3 route rules
    VirtualService = ProtoSchema{
      Type:        "virtual-service",
      ......
    }
    // Gateway describes a gateway (how a proxy is exposed on the network)
    Gateway = ProtoSchema{
      Type:        "gateway",
      ......
    }
    // IngressRule describes ingress rules
    IngressRule = ProtoSchema{
      Type:        "ingress-rule",
      ......
    }
)
```

> 做过新手任务的同学，应该都很熟悉上面的```Type```，就是配置信息里面的 ```kind```，配置信息保存进k8s后，会被pilot-discovery通过api-server爬过来进行缓存。
```
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: reviews
  ......
```

- 从k8s获取的服务注册信息
```
> istio.io/istio/pilot/pkg/serviceregistry/kube/controller.go #102
func NewController(client kubernetes.Interface, options ControllerOptions) *Controller {
    ......
  out.services = out.createInformer(&v1.Service{}, "Service", options.ResyncPeriod,
    func(opts meta_v1.ListOptions) (runtime.Object, error) {
      return client.CoreV1().Services(options.WatchedNamespace).List(opts)
    },
    func(opts meta_v1.ListOptions) (watch.Interface, error) {
      return client.CoreV1().Services(options.WatchedNamespace).Watch(opts)
    })
    ......
  out.nodes = out.createInformer(&v1.Node{}, "Node", options.ResyncPeriod,
    func(opts meta_v1.ListOptions) (runtime.Object, error) {
      return client.CoreV1().Nodes().List(opts)
    },
    func(opts meta_v1.ListOptions) (watch.Interface, error) {
      return client.CoreV1().Nodes().Watch(opts)
    })
    ......
  return out
}
```

> 还有其他数据不一一列出，从上面可以看出，建立缓存都是通过List和Watch方式进行（istio的配置数据也一样），List：第一次初始化数据，Watch：通过轮询的方式获取数据并缓存。

- 转化成的请求地址 
```
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/httpapispecs?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/servicerolebindings?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/networking.istio.io/v1alpha3/virtualservices?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/quotaspecbindings?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/serviceroles?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/networking.istio.io/v1alpha3/serviceentries?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/routerules?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/egressrules?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/authentication.istio.io/v1alpha1/policies?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/httpapispecbindings?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/networking.istio.io/v1alpha3/destinationrules?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/quotaspecs?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/networking.istio.io/v1alpha3/gateways?limit=500&resourceVersion=0
https://{k8s.ip}:443/apis/config.istio.io/v1alpha2/destinationpolicies?limit=500&resourceVersion=0

https://{k8s.ip}:443/api/v1/nodes?limit=500&resourceVersion=0
https://{k8s.ip}:443/api/v1/namespaces/istio-system/configmaps/istio-ingress-controller-leader-istio
https://{k8s.ip}:443/api/v1/services?limit=500&resourceVersion=0
https://{k8s.ip}:443/api/v1/endpoints?limit=500&resourceVersion=0
https://{k8s.ip}:443/api/v1/pods?limit=500&resourceVersion=0
```

#### key的生成
> 在pilot-discovery中把缓存数据分了二大类，一类istio配置信息，另一类服务注册信息。这二类又进行了细分，分别为virtualservices，routerules，nodes，pods等，最后再以```k8s空间/应用名```作为下标缓存数据。
```
k8s.io/client-go/tools/cache/store.go #76
func MetaNamespaceKeyFunc(obj interface{}) (string, error) {
  if key, ok := obj.(ExplicitKey); ok {
    return string(key), nil
  }
  meta, err := meta.Accessor(obj)
  if err != nil {
    return "", fmt.Errorf("object has no meta: %v", err)
  }
  if len(meta.GetNamespace()) > 0 {
    return meta.GetNamespace() + "/" + meta.GetName(), nil
  }
  return meta.GetName(), nil
}

default/sleep
kube-system/grafana
istio-system/servicegraph
```

#### 存储数据

- List 和 Watch

> 上面提到建立缓存都是通过List和Watch方式进行，来看看它的实现。
```
k8s.io/client-go/tools/cache/reflector.go #239
func (r *Reflector) ListAndWatch(stopCh <-chan struct{}) error {
  ......
  list, err := r.listerWatcher.List(options)
  ......
  resourceVersion = listMetaInterface.GetResourceVersion()
  items, err := meta.ExtractList(list)
  ......
  //缓存数据
  if err := r.syncWith(items, resourceVersion); err != nil {
    return fmt.Errorf("%s: Unable to sync list result: %v", r.name, err)
  }
  ......
  for {
    ......
    w, err := r.listerWatcher.Watch(options)
    ......
    if err := r.watchHandler(w, &resourceVersion, resyncerrc, stopCh); err != nil {
      ......
      return nil
    }
  }
}
```
- 如何更新缓存
> List可以看做第一次初始化数据，Watch更像是监听数据的变化状态：添加，修改和删除。针对这些状态对缓存的数据做增、删、改。

```
k8s.io/client-go/tools/cache/reflector.go #358
func (r *Reflector) watchHandler(w watch.Interface, resourceVersion *string, errc chan error, stopCh <-chan struct{}) error {
......
loop:
  for {
    select {
    case <-stopCh:
      return errorStopRequested
    case err := <-errc:
      return err
    case event, ok := <-w.ResultChan():
      ......
      switch event.Type {
      case watch.Added:
        err := r.store.Add(event.Object)
        ......
      case watch.Modified:
        err := r.store.Update(event.Object)
        ......
      case watch.Deleted:
      ......
        err := r.store.Delete(event.Object)
        ......
      default:
        utilruntime.HandleError(fmt.Errorf("%s: unable to understand watch event %#v", r.name, event))
      }
      ......
    }
  }
  ......
  return nil
}
```

- 限速访问 --令牌桶算法
> 刚刚看到监听数据的变化是通过for{} 不断请求k8s的api-server接口，如果不加限制，那就成了DDOS攻击了，所以pilot-discovery使用了[流量控制](https://github.com/kubernetes/client-go/blob/master/util/flowcontrol/throttle.go)。

```
k8s.io/client-go/rest/config.go
const (
  DefaultQPS   float32 = 5.0
  DefaultBurst int     = 10
)
```

> 这样理解这个配置吧，如果1秒内访问次数大于10，那么在接下来的访问中一秒最多只能访问5次。

```
k8s.io/client-go/rest/request.go #616
func (r *Request) request(fn func(*http.Request, *http.Response)) error {
  ......
  retries := 0
  for {
    ......
    if retries > 0 {
      ......
      //使用令牌桶算法
      r.tryThrottle()
    }
    resp, err := client.Do(req)
    ......
    done := func() bool {
      ......
      retries++
      ......
  }
}
```

- 协程安全map
> 在golang中使用内存key/value缓存非常简单，定义变量 ```map[string]interface{}```，再往里面放入数据就可以了。但是map结构为非协程安全，所以像pilot-discovery这种小型数据库，同时存在读和写，如果不加上锁，很容易出现争抢共享资源问题。所以需要加锁：[thread_safe_store.go](https://github.com/kubernetes/client-go/blob/master/tools/cache/thread_safe_store.go)
```
type ThreadSafeStore interface {
  Add(key string, obj interface{})
  Update(key string, obj interface{})
  Delete(key string)
  Get(key string) (item interface{}, exists bool)
  List() []interface{}
  ListKeys() []string
  Replace(map[string]interface{}, string)
  Index(indexName string, obj interface{}) ([]interface{}, error)
  IndexKeys(indexName, indexKey string) ([]string, error)
  ListIndexFuncValues(name string) []string
  ByIndex(indexName, indexKey string) ([]interface{}, error)
  GetIndexers() Indexers

  // AddIndexers adds more indexers to this store.  If you call this after you already have data
  // in the store, the results are undefined.
  AddIndexers(newIndexers Indexers) error
  Resync() error
}
```

## 提供接口

> 不管是v1 API还是v2 API，都是基于基础缓存的数据，按照envoy的[接口文档](https://www.envoyproxy.io/docs/envoy/latest/api-v1/api)，把数据拼接成envoy想要的数据。

#### 暴露v1 API RESTFUL
- 暴露的接口
> pilot-discovery暴露了```SDS/CDS/RDS/LDS```接口，envoy再使用轮询的方式，通过这些接口获取配置信息
```
istio.io/istio/pilot/pkg/proxy/envoy/v1/discovery.go #376
func (ds *DiscoveryService) Register(container *restful.Container) {
  ws := &restful.WebService{}
  ws.Produces(restful.MIME_JSON)
  ......
  ws.Route(ws.
    GET(fmt.Sprintf("/v1/registration/{%s}", ServiceKey)).
    To(ds.ListEndpoints).
    Doc("SDS registration").
    Param(ws.PathParameter(ServiceKey, "tuple of service name and tag name").DataType("string")))
  ......
  ws.Route(ws.
    GET(fmt.Sprintf("/v1/clusters/{%s}/{%s}", ServiceCluster, ServiceNode)).
    To(ds.ListClusters).
    Doc("CDS registration").
    Param(ws.PathParameter(ServiceCluster, "client proxy service cluster").DataType("string")).
    Param(ws.PathParameter(ServiceNode, "client proxy service node").DataType("string")))
  ......
  ws.Route(ws.
    GET(fmt.Sprintf("/v1/routes/{%s}/{%s}/{%s}", RouteConfigName, ServiceCluster, ServiceNode)).
    To(ds.ListRoutes).
    Doc("RDS registration").
    Param(ws.PathParameter(RouteConfigName, "route configuration name").DataType("string")).
    Param(ws.PathParameter(ServiceCluster, "client proxy service cluster").DataType("string")).
    Param(ws.PathParameter(ServiceNode, "client proxy service node").DataType("string")))
  ......
  ws.Route(ws.
    GET(fmt.Sprintf("/v1/listeners/{%s}/{%s}", ServiceCluster, ServiceNode)).
    To(ds.ListListeners).
    Doc("LDS registration").
    Param(ws.PathParameter(ServiceCluster, "client proxy service cluster").DataType("string")).
    Param(ws.PathParameter(ServiceNode, "client proxy service node").DataType("string")))
  ......
  container.Add(ws)
}
```

- 建立二级缓存
> 这里的缓存可以这样理解：我们平常开发中，从数据库获取数据，经过逻辑处理，再把最终结果进行缓存，返回给客户端，下次进来，就从缓存获取数据。同理v1 API的接口从基础缓存获取了数据后，把这些数据拼接成envoy需要的格式数据，再把这些数据缓存，返回给envoy。

1. ListEndpoints(EDS)
> 其他几个接口方式一样，不一一列出
```
istio.io/istio/pilot/pkg/proxy/envoy/v1/discovery.go #567
> func (ds *DiscoveryService) ListEndpoints(request *restful.Request, response *restful.Response) {
  ......
  key := request.Request.URL.String()
  out, resourceCount, cached := ds.sdsCache.cachedDiscoveryResponse(key)
  //没有缓存
  if !cached {
    /**
    逻辑处理
    **/
    ......
    resourceCount = uint32(len(endpoints))
    if resourceCount > 0 {
      //缓存数据
      ds.sdsCache.updateCachedDiscoveryResponse(key, resourceCount, out)
    }
  }
  observeResources(methodName, resourceCount)
  writeResponse(response, out)
}
```

#### 暴露v2 API GRPC

- [GRPC 双向流](http://doc.oschina.net/grpc?t=60133)
> 我也是刚刚接触GRPC的双向流，我对它的理解是：一个长连接，客户端和服务端可以相互交互。在这里的用法是，客户端envoy打开一个GRPC连接，初始时pilot-discovery把数据响应给envoy，接下来，如果有数据变动，pilot-discovery通过GRPC把数据推给envoy。

- ADS聚合接口

> 聚合接口就是把```SDS/CDS/RDS/LDS```的配置数据都放在一个接口上。实现有点长，缩减只剩一个接口，但方式是一样的。
```
istio.io/istio/pilot/pkg/proxy/envoy/v2/ads.go #237
func (s *DiscoveryServer) StreamAggregatedResources(stream ads.AggregatedDiscoveryService_StreamAggregatedResourcesServer) error {
  ......
  var receiveError error
  reqChannel := make(chan *xdsapi.DiscoveryRequest, 1)
  go receiveThread(con, reqChannel, &receiveError)

  for {
    // Block until either a request is received or the ticker ticks
    select {
    case discReq, ok = <-reqChannel:
      ......
      switch discReq.TypeUrl {
      case ClusterType:
      ......
      case ListenerType:
      ......
      case RouteType:
      ......
      case EndpointType:
      ......
        //推送数据
        err := s.pushEds(con)
        if err != nil {
          return err
        }
        ......
      }
    ......
    //通过监听事件触发推送数据
    case <-con.pushChannel:
      ......
      if len(con.Clusters) > 0 {
        err := s.pushEds(con)
        if err != nil {
          return err
        }
      }
      ......
    }
  }
}
```

#### 清二级缓存和触发推送

- 主动触发
> 清除二级缓存和触发推送在这里其实都是同一个触发点：就是数据变动的时候。数据的变动应该是无序的，但是在更新配置的时候应该井然有序的进行。所以这里使用了[任务队列](https://github.com/istio/istio/blob/master/pilot/pkg/serviceregistry/kube/queue.go)，让事件一件一件接着做。

1. 初始化List和Watch，注册Add，Update，Delete事件。
```
istio.io/istio/pilot/pkg/config/kube/crd/controller.go #133
func (c *controller) createInformer(
  o runtime.Object,
  otype string,
  resyncPeriod time.Duration,
  lf cache.ListFunc,
  wf cache.WatchFunc) cacheHandler {
  ......
  informer.AddEventHandler(
    cache.ResourceEventHandlerFuncs{
      AddFunc: func(obj interface{}) {
        ......
        c.queue.Push(kube.NewTask(handler.Apply, obj, model.EventAdd))
      },
      ......
    })
  return cacheHandler{informer: informer, handler: handler}
}
```
> 当事件被触发都会执行```handler.Apply```，再执行注册的方法。
```
istio.io/istio/pilot/pkg/serviceregistry/kube/queue.go #142
func (ch *ChainHandler) Apply(obj interface{}, event model.Event) error {
  for _, f := range ch.funcs {
    if err := f(obj, event); err != nil {
      return err
    }
  }
  return nil
}
```

2. 注册方法
```
istio.io/istio/pilot/pkg/proxy/envoy/v1/discovery.go #328
func NewDiscoveryService(ctl model.Controller, configCache model.ConfigStoreCache,
  environment model.Environment, o DiscoveryServiceOptions) (*DiscoveryService, error) {
  ......
  serviceHandler := func(*model.Service, model.Event) { out.clearCache() }
  if err := ctl.AppendServiceHandler(serviceHandler); err != nil {
    return nil, err
  }
  instanceHandler := func(*model.ServiceInstance, model.Event) { out.clearCache() }
  if err := ctl.AppendInstanceHandler(instanceHandler); err != nil {
    return nil, err
  }

  if configCache != nil {
    ......
    configHandler := func(model.Config, model.Event) { out.clearCache() }
    for _, descriptor := range model.IstioConfigTypes {
      configCache.RegisterEventHandler(descriptor.Type, configHandler)
    }
  }

  return out, nil
}
```
> 方法 ```out.clearCache()```，实现了清二级缓存和推送数据
```
istio.io/istio/pilot/pkg/proxy/envoy/v1/discovery.go #480
func (ds *DiscoveryService) clearCache() {
  ......
  //清二级缓存
  ds.sdsCache.clear()
  ds.cdsCache.clear()
  ds.rdsCache.clear()
  ds.ldsCache.clear()
  if V2ClearCache != nil {
    //把数据推送到envoy
    V2ClearCache()
  }
}
```

- 手动触发

> 在pilot-discovery开放了一个清二级缓存的接口。
```
istio.io/istio/pilot/pkg/proxy/envoy/v1/discovery.go #436
func (ds *DiscoveryService) Register(container *restful.Container) {
  ws := &restful.WebService{}
  ws.Produces(restful.MIME_JSON)
  ......
  ws.Route(ws.
    POST("/cache_stats_delete").
    To(ds.ClearCacheStats).
    Doc("Clear discovery service cache stats"))
  container.Add(ws)
}
```



## 小知识

#### 万恶的panic
> 在开发使用golang的过程中或多或少都接触过panic。例如引入了一些喜欢用panic的第三包，断言错误等触发了panic，导致整个服务都挂掉。为了避免这些问题，我们一般都是使用```recover```来接收panic，但一直觉得自己的处理方式不是很好。所以这次源码分析特意看了[k8s的go客户端](https://github.com/kubernetes/client-go)是如何处理panic问题，毕竟是Google出品。

```
k8s.io/apimachinery/pkg/util/runtime/runtime.go #47
func HandleCrash(additionalHandlers ...func(interface{})) {
  if r := recover(); r != nil {
    //默认会打印 出现panic问题的文件和行数
    for _, fn := range PanicHandlers {
      fn(r)
    }
    //留给使用方，出现了panic你还想如何处理
    for _, fn := range additionalHandlers {
      fn(r)
    }
    //如果你确认，可以直接panic
    if ReallyCrash {
      // Actually proceed to panic.
      panic(r)
    }
  }
}
```
> 从上面看出k8s客户端的处理方式和我们的想法一样，不过它的封装更友好。在k8s的go客户端中```HandleCrash```，更喜欢和for{}一起使用。
```
k8s.io/apimachinery/pkg/watch/streamwatcher.go #88
func (sw *StreamWatcher) receive() {
  ......
  defer utilruntime.HandleCrash()
  for {
    ......
  }
}

k8s.io/client-go/tools/record/event.go #224
func (eventBroadcaster *eventBroadcasterImpl) StartEventWatcher(eventHandler func(*v1.Event)) watch.Interface {
  ......
  go func() {
    defer utilruntime.HandleCrash()
    for {
      ......
    }
  }()
  return watcher
}
```

## 结语

> 这次的源码分析中，不单单了解了pilot-discovery的设计实现，还通过[k8s的go客户端](https://github.com/kubernetes/client-go)学习到了延迟队列，流量控制，协程安全数据库等相关的实现和应用场景，收获不少。




  [1]: https://upload.cc/i1/2018/06/21/maUVCA.png
