package otel2influx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"golang.org/x/exp/maps"

	"github.com/influxdata/influxdb-observability/common"
)

type metricWriterTelegrafPrometheusV2 struct {
	logger common.Logger
}

func (c *metricWriterTelegrafPrometheusV2) enqueueMetric(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, metric pmetric.Metric, batch InfluxWriterBatch) error {
	// Ignore metric.Description() and metric.Unit() .
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		return c.enqueueGauge(ctx, resource, instrumentationScope, metric.Name(), metric.Gauge(), batch)
	case pmetric.MetricTypeSum:
		if metric.Sum().IsMonotonic() && metric.Sum().AggregationTemporality() == pmetric.AggregationTemporalityCumulative {
			return c.enqueueCounterFromSum(ctx, resource, instrumentationScope, metric.Name(), metric.Sum(), batch)
		}
		return c.enqueueGaugeFromSum(ctx, resource, instrumentationScope, metric.Name(), metric.Sum(), batch)
	case pmetric.MetricTypeHistogram:
		return c.enqueueHistogram(ctx, resource, instrumentationScope, metric.Name(), metric.Histogram(), batch)
	case pmetric.MetricTypeSummary:
		return c.enqueueSummary(ctx, resource, instrumentationScope, metric.Name(), metric.Summary(), batch)
	case pmetric.MetricTypeEmpty:
		return nil
	default:
		return fmt.Errorf("unknown metric type %q", metric.Type())
	}
}

func (c *metricWriterTelegrafPrometheusV2) initMetricTagsAndTimestamp(dataPoint basicDataPoint, tags map[string]string) (map[string]string, map[string]interface{}, time.Time, error) {
	ts := dataPoint.Timestamp().AsTime()
	if ts.IsZero() {
		return nil, nil, time.Time{}, errors.New("metric has no timestamp")
	}

	fields := make(map[string]interface{})
	if dataPoint.StartTimestamp() != 0 {
		fields[common.AttributeStartTimeUnixNano] = int64(dataPoint.StartTimestamp())
	}

	tags = maps.Clone(tags)
	dataPoint.Attributes().Range(func(k string, v pcommon.Value) bool {
		if k != "" {
			if strings.HasPrefix(k, "field_") {
				k = strings.TrimPrefix(k, "field_")
				switch v.Type() {
				case pcommon.ValueTypeStr:
					fields[k] = v.Str()
				case pcommon.ValueTypeInt:
					fields[k] = v.Double()
				case pcommon.ValueTypeDouble:
					fields[k] = v.Double()
				case pcommon.ValueTypeBool:
					fields[k] = v.Bool()
				default:
				}

			} else {
				tags[k] = v.AsString()
			}
		}
		return true
	})
	return tags, fields, ts, nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueGauge(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, measurement string, gauge pmetric.Gauge, batch InfluxWriterBatch) error {
	tags := ResourceToTags(resource, make(map[string]string))
	tags = InstrumentationScopeToTags(instrumentationScope, tags)

	for i := 0; i < gauge.DataPoints().Len(); i++ {
		dataPoint := gauge.DataPoints().At(i)
		tags, fields, ts, err := c.initMetricTagsAndTimestamp(dataPoint, tags)
		if err != nil {
			return err
		}

		switch dataPoint.ValueType() {
		case pmetric.NumberDataPointValueTypeEmpty:
			continue
		case pmetric.NumberDataPointValueTypeDouble:
			fields[measurement] = dataPoint.DoubleValue()
		case pmetric.NumberDataPointValueTypeInt:
			fields[measurement] = dataPoint.IntValue()
		default:
			return fmt.Errorf("unsupported gauge data point type %d", dataPoint.ValueType())
		}

		if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeGauge); err != nil {
			return fmt.Errorf("failed to write point for gauge: %w", err)
		}

		err = c.enqueueExemplars(ctx, batch, measurement, dataPoint.Exemplars())
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueGaugeFromSum(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, measurement string, sum pmetric.Sum, batch InfluxWriterBatch) error {
	tags := ResourceToTags(resource, make(map[string]string))
	tags = InstrumentationScopeToTags(instrumentationScope, tags)

	for i := 0; i < sum.DataPoints().Len(); i++ {
		dataPoint := sum.DataPoints().At(i)
		tags, fields, ts, err := c.initMetricTagsAndTimestamp(dataPoint, tags)
		if err != nil {
			return err
		}

		switch dataPoint.ValueType() {
		case pmetric.NumberDataPointValueTypeEmpty:
			continue
		case pmetric.NumberDataPointValueTypeDouble:
			fields[measurement] = dataPoint.DoubleValue()
		case pmetric.NumberDataPointValueTypeInt:
			fields[measurement] = dataPoint.IntValue()
		default:
			return fmt.Errorf("unsupported sum (as gauge) data point type %d", dataPoint.ValueType())
		}

		if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeGauge); err != nil {
			return fmt.Errorf("failed to write point for sum (as gauge): %w", err)
		}

		err = c.enqueueExemplars(ctx, batch, measurement, dataPoint.Exemplars())
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueCounterFromSum(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, measurement string, sum pmetric.Sum, batch InfluxWriterBatch) error {
	tags := ResourceToTags(resource, make(map[string]string))
	tags = InstrumentationScopeToTags(instrumentationScope, tags)

	for i := 0; i < sum.DataPoints().Len(); i++ {
		dataPoint := sum.DataPoints().At(i)
		tags, fields, ts, err := c.initMetricTagsAndTimestamp(dataPoint, tags)
		if err != nil {
			return err
		}

		switch dataPoint.ValueType() {
		case pmetric.NumberDataPointValueTypeEmpty:
			continue
		case pmetric.NumberDataPointValueTypeDouble:
			fields[measurement] = dataPoint.DoubleValue()
		case pmetric.NumberDataPointValueTypeInt:
			fields[measurement] = dataPoint.IntValue()
		default:
			return fmt.Errorf("unsupported sum data point type %d", dataPoint.ValueType())
		}

		if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeSum); err != nil {
			return fmt.Errorf("failed to write point for sum: %w", err)
		}

		err = c.enqueueExemplars(ctx, batch, measurement, dataPoint.Exemplars())
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueHistogram(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, measurement string, histogram pmetric.Histogram, batch InfluxWriterBatch) error {
	tags := ResourceToTags(resource, make(map[string]string))
	tags = InstrumentationScopeToTags(instrumentationScope, tags)

	for i := 0; i < histogram.DataPoints().Len(); i++ {
		dataPoint := histogram.DataPoints().At(i)
		tags, fields, ts, err := c.initMetricTagsAndTimestamp(dataPoint, tags)
		if err != nil {
			return err
		}

		fields[measurement+common.MetricHistogramCountSuffix] = float64(dataPoint.Count())
		if dataPoint.HasSum() {
			fields[measurement+common.MetricHistogramSumSuffix] = dataPoint.Sum()
		}
		if dataPoint.HasMin() {
			fields[measurement+common.MetricHistogramMinSuffix] = dataPoint.Min()
		}
		if dataPoint.HasMax() {
			fields[measurement+common.MetricHistogramMaxSuffix] = dataPoint.Max()
		}
		if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeHistogram); err != nil {
			return fmt.Errorf("failed to write point for histogram: %w", err)
		}

		bucketCounts, explicitBounds := dataPoint.BucketCounts(), dataPoint.ExplicitBounds()
		if bucketCounts.Len() > 0 &&
			bucketCounts.Len() != explicitBounds.Len() &&
			bucketCounts.Len() != explicitBounds.Len()+1 {
			// The infinity bucket is not used in this schema,
			// so accept input if that particular bucket is missing.
			return fmt.Errorf("invalid metric histogram bucket counts qty %d vs explicit bounds qty %d", bucketCounts.Len(), explicitBounds.Len())
		}
		for j := 0; j < bucketCounts.Len(); j++ {
			tags := maps.Clone(tags)
			fields := make(map[string]interface{})

			var bucketCount uint64
			for k := 0; k <= j; k++ {
				bucketCount += bucketCounts.At(k)
			}

			var boundTagValue string
			if explicitBounds.Len() > j {
				boundTagValue = strconv.FormatFloat(explicitBounds.At(j), 'f', -1, 64)
			} else {
				boundTagValue = common.MetricHistogramInfFieldKey
			}
			tags[common.MetricHistogramBoundKeyV2] = boundTagValue
			fields[measurement+common.MetricHistogramBucketSuffix] = float64(bucketCount)

			if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeHistogram); err != nil {
				return fmt.Errorf("failed to write point for histogram: %w", err)
			}
		}

		err = c.enqueueExemplars(ctx, batch, measurement, dataPoint.Exemplars())
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueSummary(ctx context.Context, resource pcommon.Resource, instrumentationScope pcommon.InstrumentationScope, measurement string, summary pmetric.Summary, batch InfluxWriterBatch) error {
	tags := ResourceToTags(resource, make(map[string]string))
	tags = InstrumentationScopeToTags(instrumentationScope, tags)

	for i := 0; i < summary.DataPoints().Len(); i++ {
		dataPoint := summary.DataPoints().At(i)
		tags, fields, ts, err := c.initMetricTagsAndTimestamp(dataPoint, tags)
		if err != nil {
			return err
		}

		fields[measurement+common.MetricSummaryCountSuffix] = float64(dataPoint.Count())
		fields[measurement+common.MetricSummarySumSuffix] = dataPoint.Sum()

		if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeSummary); err != nil {
			return fmt.Errorf("failed to write point for summary: %w", err)
		}

		for j := 0; j < dataPoint.QuantileValues().Len(); j++ {
			tags := maps.Clone(tags)
			fields := make(map[string]interface{})

			valueAtQuantile := dataPoint.QuantileValues().At(j)
			quantileTagValue := strconv.FormatFloat(valueAtQuantile.Quantile(), 'f', -1, 64)
			tags[common.MetricSummaryQuantileKeyV2] = quantileTagValue
			fields[measurement] = float64(valueAtQuantile.Value())

			if err = batch.EnqueuePoint(ctx, common.MeasurementPrometheus, tags, fields, ts, common.InfluxMetricValueTypeSummary); err != nil {
				return fmt.Errorf("failed to write point for summary: %w", err)
			}
		}
	}

	return nil
}

func (c *metricWriterTelegrafPrometheusV2) enqueueExemplars(ctx context.Context, batch InfluxWriterBatch, measurement string, exemplars pmetric.ExemplarSlice) error {
	for j := 0; j < exemplars.Len(); j++ {
		exemplar := exemplars.At(j)
		var fields map[string]interface{}
		switch exemplar.ValueType() {
		case pmetric.ExemplarValueTypeEmpty:
			continue
		case pmetric.ExemplarValueTypeDouble:
			fields = map[string]interface{}{common.MetricGaugeFieldKey: exemplar.DoubleValue()}
		case pmetric.ExemplarValueTypeInt:
			fields = map[string]interface{}{common.MetricGaugeFieldKey: exemplar.IntValue()}
		default:
			return fmt.Errorf("unsupported exemplar value type %d", exemplar.ValueType())
		}
		if exemplar.TraceID().IsEmpty() || exemplar.SpanID().IsEmpty() {
			continue
		}

		tags := make(map[string]string, exemplar.FilteredAttributes().Len()+2)
		exemplar.FilteredAttributes().Range(func(k string, v pcommon.Value) bool {
			tags[k] = v.AsString()
			return true
		})
		tags[common.AttributeTraceID] = exemplar.TraceID().String()
		tags[common.AttributeSpanID] = exemplar.SpanID().String()

		if err := batch.EnqueuePoint(ctx, measurement+common.MetricExemplarSuffix, tags, fields, exemplar.Timestamp().AsTime(), common.InfluxMetricValueTypeUntyped); err != nil {
			return fmt.Errorf("failed to write point for exemplar: %w", err)
		}
	}
	return nil
}
