![image](http://jaeger.readthedocs.io/en/latest/images/architecture.png)

## 前言

> &emsp; 随着公司的发展，业务不断的增加，模块的不断拆分，系统间业务调用就变得越复杂，对定位线上故障带来很大困难。整个调用链不透明，犹如系统被蒙上一块黑纱，当线上遇到故障时，整个技术部就陷入痛苦的漩涡。这时候分布式追踪系统应运而生，如揭开了黑纱，让阳光照进黑暗。

#### 分布式系统调用过程

![image](http://bigbully.github.io/Dapper-translation/images/img1.png)

## [opentracing 协议](http://opentracing.io/)

> &emsp;opentracing是一套分布式追踪协议，与平台，语言无关，统一接口，方便开发接入不同的分布式追踪系统。

![image](https://upload.cc/i/aV6NzC.png)

#### 简单理解opentracing
> 一个完整的opentracing调用链包含 Trace + span + 无限极分类

* Trace：追踪对象，一个Trace代表了一个服务或者流程在系统中的执行过程，如：test.com，redis，mysql等执行过程。一个Trace由多个span组成
* span：记录Trace在执行过程中的信息，如：查询的sql，请求的HTTP地址，RPC调用，开始、结束、间隔时间等。
* 无限极分类：服务与服务之间使用无限极分类的方式，通过HTTP头部或者请求地址传输到最低层，从而把整个调用链串起来。

#### 相关文档
* [官方文档](https://github.com/opentracing/specification)
* [OpenTracing语义规范(中文版)](https://segmentfault.com/a/1190000008895129)
* [OpenTracing语义惯例](https://opentracing-contrib.github.io/opentracing-specification-zh/semantic_conventions.html)
* [opentracing文档中文版 ( 翻译 ) 吴晟](https://wu-sheng.gitbooks.io/opentracing-io/content/)

## 分布式追踪系统Jaeger

> &emsp;[Jaeger](https://uber.github.io/jaeger/)是Uber开发的一套分布式追踪系统，已在Uber大规模使用。并在[2017-9-13 加入CNCF 开源组织](https://www.cncf.io/blog/2017/09/13/cncf-hosts-jaeger/)。使用Jaeger可以非常直观的展示整个分布式系统的调用链，由此可以很好发现和解决问题：

![image](http://jaeger.readthedocs.io/en/latest/images/traces-ss.png)

## 作用
- 分布式环境下信息传播
- 分布式交易监控
- 展示跨进程调用链
- 性能优化
- 定位问题

## 特性

* 使用udp传输数据，相对于HTTP，优点在于，不用担心Jaeger服务宕机或者网络传输有问题而影响正常的业务。缺点就是丢包，影响了整条调用链。
* 数据通过Thrift进行序列化，和json对比

接口 | Thrift/byte | json/byte | 节省
---|--- | --- | ---
接口1 | 987 | 2396 | 约1.5倍
接口2 | 1212 | 2916 | 约1.4倍
接口3 | 12830 | 18893 | 约40%
接口4 | 17158 | 22465 | 约24%
接口5 | 11025 | 14282 | 约23%

> &emsp;从中可以看出thrift相对json减少了不少空间。在我们采集公司接口的数据大小都集中在10~20k，所以使用thrift会更有优势。

* 采集策略

> Jaeger 官方提供了多种采集策略，使用者可以按需选择使用 

1. ConstSampler，全量采集
2. ProbabilisticSampler ，概率采集，默认万份之一
3. RateLimitingSampler ，限速采集，每秒只能采集一定量的数据
4. RemotelyControlledSampler ，一种动态采集策略，根据当前系统的访问量调节采集策略
 

* [客户端](https://github.com/jaegertracing/jaeger/issues/366)
1. [Go](https://github.com/uber/jaeger-client-go)
2. [Java](https://github.com/jaegertracing/jaeger-client-java)
3. [node](https://github.com/jaegertracing/jaeger-client-node)
4. [python](https://github.com/jaegertracing/jaeger-client-python)
5. [php](https://github.com/jukylin/jaeger-php)

 
> 官方提供了 go，java，node，python客户端，其他客户端还在开放中，[php客户端](https://github.com/jukylin/jaeger-php)为个人开发【欢迎start】



## 部署

#### 快速部署 ------ All in one Docker image

> &emsp;all-in-one 是Uber官方打包好的镜像，可以直接部署使用，但是只能用于测试环境，不能用于线上，因为它把数据放入了内存。

```
docker run -d -e COLLECTOR_ZIPKIN_HTTP_PORT=9411 -p5775:5775/udp -p6831:6831/udp -p6832:6832/udp \
  -p5778:5778 -p16686:16686 -p14268:14268 -p9411:9411 jaegertracing/all-in-one:latest
```

> 通过 http://localhost:16686 可以在浏览器查看 Jaeger的后台

#### 官方提供的使用例子，需要go环境

```
go get github.com/uber/jaeger
cd $GOPATH/src/github.com/uber/jaeger
make install_examples
cd examples/hotrod
go run ./main.go all
```

> http://localhost:8080 浏览器打开查看

##  cassandra + docker  部署，单机模式

> docker run -itd \
--name=cassandra  -p9042:9042 \
-v /data/cassandra:/var/lib/cassandra \
cassandra

#### 进入容器建立表空间
> 按照官方[脚本](http://jaeger.readthedocs.io/en/latest/deployment/#cassandra)整理了一份建表语句[jaeger_tables](http://note.youdao.com/noteshare?id=ec3a48e4793ce72f17c41f40681a6444)，进入cassandra，执行语句即可创建所需的表。

> keyspaces：jaeger_v1_dc

## 运行 jaeger-query

> docker run -itd --network=bridge \
  --name=jaeger-query \
  -p16686:16686 \
  jaegertracing/jaeger-query \
  /go/bin/query-linux \
  --span-storage.type=cassandra \
  --cassandra.keyspace=jaeger_v1_dc \
  --cassandra.servers={{ cassandra }}:9042 \
  --query.static-files=/go/jaeger-ui/

## 运行 jaeger-collector

> docker run -itd --network=bridge  \
  --name=jaeger-collector \
  -p14267:14267 \
  -p14268:14268 \
  -p9411:9411 \
  jaegertracing/jaeger-collector \
  /go/bin/collector-linux \
  --span-storage.type=cassandra \
  --cassandra.keyspace=jaeger_v1_dc \
  --cassandra.servers={{ cassandra }}:9042
  
## 运行 jaeger-agent

> docker run \
  -itd --network=bridge \
  --name=jaeger-agent \
  -p5775:5775/udp \
  -p6831:6831/udp \
  -p6832:6832/udp \
  -p5778:5778/tcp \
  jaegertracing/jaeger-agent \
  /go/bin/agent-linux --collector.host-port={{ jaeger-collector }}:14267
  

## 跨语言调用案例

* php
* Hprose
* Go【beego】

#### beego

* install beego
```
go get github.com/astaxie/beego
```

* Download trace_example

```
git clone git@github.com:jukylin/trace_example.git
cd  trace_example
bee run trace_example
```

#### PHP

* [install jaeger-php ](https://github.com/jukylin/jaeger-php#install)

* Run Hprose.php

```
cd vendor/jukylin/jaeger-php/example
php Hprose.php
```

#### Trace Result View

![image](https://upload.cc/i/aht9Yr.png)

## PS

- [优步分布式追踪技术再度精进](http://www.infoq.com/cn/articles/evolving-distributed-tracing-at-uber-engineering)
- Jaeger还在发展中，官方也希望得到[社区的支持](http://jaeger.readthedocs.io/en/latest/roadmap/)。
- Jaeger和[Jaeger-php](https://github.com/jukylin/jaeger-php)，在公司测试环境运行了1个多月，于2017-10-18发布上线。
- 选择好采集策略，日志数据会爆炸性增长。
