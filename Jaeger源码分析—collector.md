## TChannel 接收 agent 提交过来的数据

> github.com/jaegertracing/jaeger/cmd/collector/app/span_handler.go #69

```
func (jbh *jaegerBatchesHandler) SubmitBatches(ctx thrift.Context, batches []*jaeger.Batch) ([]*jaeger.BatchSubmitResponse, error) {
	responses := make([]*jaeger.BatchSubmitResponse, 0, len(batches))
	for _, batch := range batches {
		mSpans := make([]*model.Span, 0, len(batch.Spans))
		for _, span := range batch.Spans {
			mSpan := jConv.ToDomainSpan(span, batch.Process)
			mSpans = append(mSpans, mSpan)
		}
		oks, err := jbh.modelProcessor.ProcessSpans(mSpans, JaegerFormatType)
		if err != nil {
			return nil, err
		}
		batchOk := true
		for _, ok := range oks {
			if !ok {
				batchOk = false
				break
			}
		}
		res := &jaeger.BatchSubmitResponse{
			Ok: batchOk,
		}
		responses = append(responses, res)
	}
	return responses, nil
}
```


## 经过处理后数据放入BoundedQueue(有界队列)

> github.com/jaegertracing/jaeger/cmd/collector/app/span_processor.go #130

```
func (sp *spanProcessor) enqueueSpan(span *model.Span, originalFormat string) bool {
	spanCounts := sp.metrics.GetCountsForFormat(originalFormat)
	spanCounts.ReceivedBySvc.ReportServiceNameForSpan(span)

	if !sp.filterSpan(span) {
		spanCounts.Rejected.Inc(int64(1))
		return true // as in "not dropped", because it's actively rejected
	}
	item := &queueItem{
		queuedTime: time.Now(),
		span:       span,
	}
	addedToQueue := sp.queue.Produce(item)
	if !addedToQueue {
		sp.metrics.ErrorBusy.Inc(1)
	}
	return addedToQueue
}

```

## 启用50个协成，处理队列消息

> github.com/jaegertracing/jaeger/pkg/queue/bounded_queue.go #53

```
func (q *BoundedQueue) StartConsumers(num int, consumer func(item interface{})) {
    var startWG sync.WaitGroup
    for i := 0; i < num; i++ {
        q.stopWG.Add(1)
        //这个WG是否多余？
        startWG.Add(1)
        go func() {
            startWG.Done()
            defer q.stopWG.Done()
            for {
                select {
                case item := <-q.items:
                    atomic.AddInt32(&q.size, -1)
                    consumer(item)
                case <-q.stopCh:
                    return
                }
            }
        }()
    }
    startWG.Wait()
}
```

> collector 在处理队列数据的时候和agent一样，处理不完会直接扔掉

> 可以通过配置参数优化

- --collector.num-workers （default 50）

- --collector.queue-size （default 2000）

- 增加collector服务节点

## 从队列拿出来后经过处理，把数据存入cassandra数据库

> github.com/jaegertracing/jaeger/cmd/collector/app/span_processor.go #101


```
func (sp *spanProcessor) saveSpan(span *model.Span) {
	startTime := time.Now()
	if err := sp.spanWriter.WriteSpan(span); err != nil {
		sp.logger.Error("Failed to save span", zap.Error(err))
	} else {
		sp.metrics.SavedBySvc.ReportServiceNameForSpan(span)
	}
	sp.metrics.SaveLatency.Record(time.Now().Sub(startTime))
}
```

> github.com/jaegertracing/jaeger/plugin/storage/cassandra/spanstore/writer.go #122


```
func (s *SpanWriter) WriteSpan(span *model.Span) error {
	ds := dbmodel.FromDomain(span)
	mainQuery := s.session.Query(
		insertSpan,
		ds.TraceID,
		ds.SpanID,
		ds.SpanHash,
		ds.ParentID,
		ds.OperationName,
		ds.Flags,
		ds.StartTime,
		ds.Duration,
		ds.Tags,
		ds.Logs,
		ds.Refs,
		ds.Process,
	)

	if err := s.writerMetrics.traces.Exec(mainQuery, s.logger); err != nil {
		return s.logError(ds, err, "Failed to insert span", s.logger)
	}
	if err := s.saveServiceNameAndOperationName(ds.ServiceName, ds.OperationName); err != nil {
		// should this be a soft failure?
		return s.logError(ds, err, "Failed to insert service name and operation name", s.logger)
	}

	if err := s.indexByTags(span, ds); err != nil {
		return s.logError(ds, err, "Failed to index tags", s.logger)
	}

	if err := s.indexBySerice(span.TraceID, ds); err != nil {
		return s.logError(ds, err, "Failed to index service name", s.logger)
	}

	if err := s.indexByOperation(span.TraceID, ds); err != nil {
		return s.logError(ds, err, "Failed to index operation name", s.logger)
	}

	if err := s.indexByDuration(ds, span.StartTime); err != nil {
		return s.logError(ds, err, "Failed to index duration", s.logger)
	}
	return nil
```

## 保存saveServiceNameAndOperationName，collector借助缓存（key/value 和 lru）

> 借助缓存，Jaeger实现不重复写入Service和OperationName，是否已经写入通过缓存判断，不查询cassandra，减少了查询压力。

> github.com/jaegertracing/jaeger/plugin/storage/cassandra/spanstore/service_names.go #69

```
func (s *ServiceNamesStorage) Write(serviceName string) error {
    var err error
    query := s.session.Query(s.InsertStmt)
    if inCache := checkWriteCache(serviceName, s.serviceNames, s.writeCacheTTL); !inCache {
        q := query.Bind(serviceName)
        err2 := s.metrics.Exec(q, s.logger)
        if err2 != nil {
        	err = err2
        }
    }
    return err
}
```

> &nbsp;&nbsp;&nbsp;&nbsp;在默认情况下ServiceName缓存长度为10000，OperationName缓存长度十万，如果超过限制重复写入。从实际上考虑这样的限制是否够用？其实Uber发展到现在也只有1000多个ServiceName，所以这个设置可以满足很多公司。

