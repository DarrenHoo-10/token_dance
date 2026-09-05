use std::collections::BTreeMap;
use std::net::IpAddr;

use adapter_sdk::{RawFrame, SourceKind};
use opentelemetry_proto::tonic::collector::logs::v1::ExportLogsServiceRequest;
use opentelemetry_proto::tonic::collector::metrics::v1::ExportMetricsServiceRequest;
use opentelemetry_proto::tonic::common::v1::{any_value, AnyValue, InstrumentationScope, KeyValue};
use opentelemetry_proto::tonic::logs::v1::LogRecord;
use opentelemetry_proto::tonic::metrics::v1::{metric, AggregationTemporality, Metric};
use opentelemetry_proto::tonic::resource::v1::Resource;
use prost::Message;
use serde::Serialize;
use serde_json::{Map, Number, Value};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

use crate::AcquisitionError;

pub const DEFAULT_OTLP_PAYLOAD_LIMIT: usize = 4 * 1024 * 1024;
pub const DEFAULT_OTLP_RESOURCE_ATTRIBUTE_LIMIT: usize = 128;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OtlpSignal {
    Metrics,
    Logs,
}

impl TryFrom<&str> for OtlpSignal {
    type Error = AcquisitionError;

    fn try_from(value: &str) -> Result<Self, Self::Error> {
        match value {
            "metrics" | "/v1/metrics" => Ok(Self::Metrics),
            "logs" | "/v1/logs" => Ok(Self::Logs),
            _ => Err(AcquisitionError::Other("unsupported_otlp_signal".into())),
        }
    }
}

#[derive(Debug, Clone)]
pub struct OtlpReceiverDriver {
    installation_id: String,
    source_id: String,
    bind_host: String,
    payload_limit: usize,
    resource_attribute_limit: usize,
    sequence: u64,
}

impl OtlpReceiverDriver {
    pub fn new(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        bind_host: impl Into<String>,
        payload_limit: usize,
    ) -> Result<Self, AcquisitionError> {
        Self::new_with_limits(
            installation_id,
            source_id,
            bind_host,
            payload_limit,
            DEFAULT_OTLP_RESOURCE_ATTRIBUTE_LIMIT,
        )
    }

    pub fn new_with_limits(
        installation_id: impl Into<String>,
        source_id: impl Into<String>,
        bind_host: impl Into<String>,
        payload_limit: usize,
        resource_attribute_limit: usize,
    ) -> Result<Self, AcquisitionError> {
        let bind_host = bind_host.into();
        let loopback = bind_host == "localhost"
            || bind_host
                .parse::<IpAddr>()
                .is_ok_and(|address| address.is_loopback());
        if !loopback {
            return Err(AcquisitionError::Other("otlp_bind_must_be_loopback".into()));
        }
        if payload_limit == 0 || payload_limit > DEFAULT_OTLP_PAYLOAD_LIMIT {
            return Err(AcquisitionError::Other("invalid_otlp_payload_limit".into()));
        }
        if resource_attribute_limit == 0
            || resource_attribute_limit > DEFAULT_OTLP_RESOURCE_ATTRIBUTE_LIMIT
        {
            return Err(AcquisitionError::Other(
                "invalid_otlp_resource_attribute_limit".into(),
            ));
        }
        Ok(Self {
            installation_id: installation_id.into(),
            source_id: source_id.into(),
            bind_host,
            payload_limit,
            resource_attribute_limit,
            sequence: 0,
        })
    }

    pub fn bind_host(&self) -> &str {
        &self.bind_host
    }

    pub fn restore_sequence(&mut self, sequence: u64) {
        self.sequence = sequence;
    }

    pub fn sequence(&self) -> u64 {
        self.sequence
    }

    pub fn accept_http_path(
        &mut self,
        path: &str,
        content_type: &str,
        payload: &[u8],
    ) -> Result<RawFrame, AcquisitionError> {
        self.accept_http(OtlpSignal::try_from(path)?, content_type, payload)
    }

    pub fn accept_http(
        &mut self,
        signal: OtlpSignal,
        content_type: &str,
        payload: &[u8],
    ) -> Result<RawFrame, AcquisitionError> {
        self.check_payload(payload)?;
        let media_type = content_type
            .split(';')
            .next()
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase();
        let lines = match media_type.as_str() {
            "application/json" => self.decode_json(signal, payload)?,
            "application/x-protobuf" | "application/protobuf" => {
                self.decode_protobuf(signal, payload)?
            }
            _ => {
                return Err(AcquisitionError::Other(
                    "unsupported_otlp_content_type".into(),
                ))
            }
        };
        self.frame(lines)
    }

    pub fn accept_json(
        &mut self,
        signal: OtlpSignal,
        payload: &[u8],
    ) -> Result<RawFrame, AcquisitionError> {
        self.check_payload(payload)?;
        let lines = self.decode_json(signal, payload)?;
        self.frame(lines)
    }

    pub fn accept_protobuf(
        &mut self,
        signal: OtlpSignal,
        payload: &[u8],
    ) -> Result<RawFrame, AcquisitionError> {
        self.check_payload(payload)?;
        let lines = self.decode_protobuf(signal, payload)?;
        self.frame(lines)
    }

    fn check_payload(&self, payload: &[u8]) -> Result<(), AcquisitionError> {
        if payload.len() > self.payload_limit {
            return Err(AcquisitionError::Other("otlp_payload_too_large".into()));
        }
        if payload.is_empty() {
            return Err(AcquisitionError::Other("empty_otlp_payload".into()));
        }
        Ok(())
    }

    fn decode_json(
        &self,
        signal: OtlpSignal,
        payload: &[u8],
    ) -> Result<Vec<Value>, AcquisitionError> {
        let mut envelope: Value = serde_json::from_slice(payload)
            .map_err(|error| AcquisitionError::Other(format!("invalid_otlp_json:{error}")))?;
        normalize_otlp_json_numbers(&mut envelope)?;
        let object = envelope
            .as_object()
            .ok_or_else(|| AcquisitionError::Other("invalid_otlp_json_envelope".into()))?;
        match signal {
            OtlpSignal::Metrics => {
                if !object.contains_key("resourceMetrics") || object.contains_key("resourceLogs") {
                    return Err(AcquisitionError::Other(
                        "invalid_otlp_metrics_json_envelope".into(),
                    ));
                }
                let request: ExportMetricsServiceRequest = serde_json::from_value(envelope)
                    .map_err(|error| {
                        AcquisitionError::Other(format!("invalid_otlp_metrics_json:{error}"))
                    })?;
                self.normalize_metrics(request)
            }
            OtlpSignal::Logs => {
                if !object.contains_key("resourceLogs") || object.contains_key("resourceMetrics") {
                    return Err(AcquisitionError::Other(
                        "invalid_otlp_logs_json_envelope".into(),
                    ));
                }
                let request: ExportLogsServiceRequest =
                    serde_json::from_value(envelope).map_err(|error| {
                        AcquisitionError::Other(format!("invalid_otlp_logs_json:{error}"))
                    })?;
                self.normalize_logs(request)
            }
        }
    }

    fn decode_protobuf(
        &self,
        signal: OtlpSignal,
        payload: &[u8],
    ) -> Result<Vec<Value>, AcquisitionError> {
        match signal {
            OtlpSignal::Metrics => ExportMetricsServiceRequest::decode(payload)
                .map_err(|error| {
                    AcquisitionError::Other(format!("invalid_otlp_metrics_protobuf:{error}"))
                })
                .and_then(|request| self.normalize_metrics(request)),
            OtlpSignal::Logs => ExportLogsServiceRequest::decode(payload)
                .map_err(|error| {
                    AcquisitionError::Other(format!("invalid_otlp_logs_protobuf:{error}"))
                })
                .and_then(|request| self.normalize_logs(request)),
        }
    }

    fn normalize_metrics(
        &self,
        request: ExportMetricsServiceRequest,
    ) -> Result<Vec<Value>, AcquisitionError> {
        let mut lines = Vec::new();
        for resource_metrics in request.resource_metrics {
            let resource = self.resource_json(
                resource_metrics.resource.as_ref(),
                &resource_metrics.schema_url,
            )?;
            for scope_metrics in resource_metrics.scope_metrics {
                let scope = scope_json(scope_metrics.scope.as_ref(), &scope_metrics.schema_url)?;
                for metric in scope_metrics.metrics {
                    normalize_metric(&mut lines, &metric, &resource, &scope)?;
                }
            }
        }
        require_records(lines, "otlp_metrics_has_no_datapoints")
    }

    fn normalize_logs(
        &self,
        request: ExportLogsServiceRequest,
    ) -> Result<Vec<Value>, AcquisitionError> {
        let mut lines = Vec::new();
        for resource_logs in request.resource_logs {
            let resource =
                self.resource_json(resource_logs.resource.as_ref(), &resource_logs.schema_url)?;
            for scope_logs in resource_logs.scope_logs {
                let scope = scope_json(scope_logs.scope.as_ref(), &scope_logs.schema_url)?;
                for record in scope_logs.log_records {
                    lines.push(normalize_log_record(&record, &resource, &scope)?);
                }
            }
        }
        require_records(lines, "otlp_logs_has_no_records")
    }

    fn resource_json(
        &self,
        resource: Option<&Resource>,
        schema_url: &str,
    ) -> Result<Value, AcquisitionError> {
        let attributes = resource.map_or(&[][..], |resource| resource.attributes.as_slice());
        if attributes.len() > self.resource_attribute_limit {
            return Err(AcquisitionError::Other(
                "otlp_resource_attribute_limit_exceeded".into(),
            ));
        }
        let mut value = Map::new();
        value.insert(
            "attributes".into(),
            Value::Object(attributes_json(attributes)?),
        );
        value.insert(
            "droppedAttributesCount".into(),
            Value::from(resource.map_or(0, |resource| resource.dropped_attributes_count)),
        );
        value.insert("schemaUrl".into(), Value::String(schema_url.to_owned()));
        Ok(Value::Object(value))
    }

    fn frame(&mut self, lines: Vec<Value>) -> Result<RawFrame, AcquisitionError> {
        let mut payload = Vec::new();
        for line in lines {
            serde_json::to_writer(&mut payload, &line)
                .map_err(|error| AcquisitionError::Other(format!("otlp_jsonl_encode:{error}")))?;
            payload.push(b'\n');
        }
        self.sequence = self.sequence.saturating_add(1);
        Ok(RawFrame {
            installation_id: self.installation_id.clone(),
            source_kind: SourceKind::Otlp,
            source_id: self.source_id.clone(),
            cursor: self.sequence.to_string(),
            payload,
        })
    }
}

fn require_records(lines: Vec<Value>, error: &str) -> Result<Vec<Value>, AcquisitionError> {
    if lines.is_empty() {
        Err(AcquisitionError::Other(error.into()))
    } else {
        Ok(lines)
    }
}

fn normalize_otlp_json_numbers(value: &mut Value) -> Result<(), AcquisitionError> {
    match value {
        Value::Array(values) => {
            for value in values {
                normalize_otlp_json_numbers(value)?;
            }
        }
        Value::Object(values) => {
            if let Some(Value::String(raw)) = values.get_mut("asInt") {
                let parsed = raw
                    .parse::<i64>()
                    .map_err(|_| AcquisitionError::Other("invalid_otlp_json_as_int".into()))?;
                *values.get_mut("asInt").expect("asInt exists") = Value::from(parsed);
            }
            for value in values.values_mut() {
                normalize_otlp_json_numbers(value)?;
            }
        }
        _ => {}
    }
    Ok(())
}

fn scope_json(
    scope: Option<&InstrumentationScope>,
    schema_url: &str,
) -> Result<Value, AcquisitionError> {
    let mut value = Map::new();
    value.insert(
        "name".into(),
        Value::String(scope.map_or("", |scope| scope.name.as_str()).to_owned()),
    );
    value.insert(
        "version".into(),
        Value::String(scope.map_or("", |scope| scope.version.as_str()).to_owned()),
    );
    let attributes = scope.map_or(&[][..], |scope| scope.attributes.as_slice());
    value.insert(
        "attributes".into(),
        Value::Object(attributes_json(attributes)?),
    );
    value.insert(
        "droppedAttributesCount".into(),
        Value::from(scope.map_or(0, |scope| scope.dropped_attributes_count)),
    );
    value.insert("schemaUrl".into(), Value::String(schema_url.to_owned()));
    Ok(Value::Object(value))
}

fn normalize_metric(
    lines: &mut Vec<Value>,
    metric: &Metric,
    resource: &Value,
    scope: &Value,
) -> Result<(), AcquisitionError> {
    match metric.data.as_ref() {
        Some(metric::Data::Gauge(gauge)) => {
            for point in &gauge.data_points {
                lines.push(metric_point(
                    metric,
                    "gauge",
                    None,
                    point,
                    &point.attributes,
                    resource,
                    scope,
                )?);
            }
        }
        Some(metric::Data::Sum(sum)) => {
            let temporality = Some(sum.aggregation_temporality);
            for point in &sum.data_points {
                let mut line = metric_point(
                    metric,
                    "sum",
                    temporality,
                    point,
                    &point.attributes,
                    resource,
                    scope,
                )?;
                line.as_object_mut()
                    .expect("metric point is an object")
                    .insert("isMonotonic".into(), Value::Bool(sum.is_monotonic));
                lines.push(line);
            }
        }
        Some(metric::Data::Histogram(histogram)) => {
            let temporality = Some(histogram.aggregation_temporality);
            for point in &histogram.data_points {
                lines.push(metric_point(
                    metric,
                    "histogram",
                    temporality,
                    point,
                    &point.attributes,
                    resource,
                    scope,
                )?);
            }
        }
        Some(metric::Data::ExponentialHistogram(histogram)) => {
            let temporality = Some(histogram.aggregation_temporality);
            for point in &histogram.data_points {
                lines.push(metric_point(
                    metric,
                    "exponentialHistogram",
                    temporality,
                    point,
                    &point.attributes,
                    resource,
                    scope,
                )?);
            }
        }
        Some(metric::Data::Summary(summary)) => {
            for point in &summary.data_points {
                lines.push(metric_point(
                    metric,
                    "summary",
                    None,
                    point,
                    &point.attributes,
                    resource,
                    scope,
                )?);
            }
        }
        None => {}
    }
    Ok(())
}

fn metric_point<T: Serialize>(
    metric: &Metric,
    metric_type: &str,
    temporality: Option<i32>,
    point: &T,
    attributes: &[KeyValue],
    resource: &Value,
    scope: &Value,
) -> Result<Value, AcquisitionError> {
    let mut value = serde_json::to_value(point)
        .map_err(|error| AcquisitionError::Other(format!("otlp_metric_normalize:{error}")))?
        .as_object()
        .cloned()
        .ok_or_else(|| AcquisitionError::Other("invalid_otlp_metric_datapoint".into()))?;
    value.insert("signal".into(), Value::String("metrics".into()));
    value.insert("name".into(), Value::String(metric.name.clone()));
    value.insert(
        "description".into(),
        Value::String(metric.description.clone()),
    );
    value.insert("unit".into(), Value::String(metric.unit.clone()));
    value.insert("metricType".into(), Value::String(metric_type.into()));
    value.insert(
        "attributes".into(),
        Value::Object(attributes_json(attributes)?),
    );
    value.insert("resource".into(), resource.clone());
    value.insert("scope".into(), scope.clone());
    if let Some(raw) = temporality {
        value.insert("aggregationTemporality".into(), Value::from(raw));
        value.insert("temporality".into(), Value::String(temporality_name(raw)));
    }
    if let Some(number) = value
        .get("asInt")
        .or_else(|| value.get("asDouble"))
        .cloned()
    {
        value.insert("value".into(), number);
    }
    if let Some(end) = value.get("timeUnixNano").cloned() {
        let timestamp = otlp_nanos_value(&end)?;
        value.insert("timestamp".into(), Value::String(timestamp));
        value.insert("endTimeUnixNano".into(), end);
    }
    Ok(Value::Object(value))
}

fn otlp_nanos_value(value: &Value) -> Result<String, AcquisitionError> {
    let nanos = match value {
        Value::String(value) => value.parse::<u64>().ok(),
        Value::Number(value) => value.as_u64(),
        _ => None,
    }
    .ok_or_else(|| AcquisitionError::Other("invalid_otlp_timestamp".into()))?;
    otlp_nanos(nanos)
}

fn otlp_nanos(nanos: u64) -> Result<String, AcquisitionError> {
    OffsetDateTime::from_unix_timestamp_nanos(i128::from(nanos))
        .map_err(|_| AcquisitionError::Other("invalid_otlp_timestamp".into()))?
        .format(&Rfc3339)
        .map_err(|_| AcquisitionError::Other("invalid_otlp_timestamp".into()))
}

fn temporality_name(value: i32) -> String {
    match AggregationTemporality::try_from(value).ok() {
        Some(AggregationTemporality::Unspecified) => "unspecified".into(),
        Some(AggregationTemporality::Delta) => "delta".into(),
        Some(AggregationTemporality::Cumulative) => "cumulative".into(),
        None => format!("unknown:{value}"),
    }
}

fn normalize_log_record(
    record: &LogRecord,
    resource: &Value,
    scope: &Value,
) -> Result<Value, AcquisitionError> {
    let mut value = serde_json::to_value(record)
        .map_err(|error| AcquisitionError::Other(format!("otlp_log_normalize:{error}")))?
        .as_object()
        .cloned()
        .ok_or_else(|| AcquisitionError::Other("invalid_otlp_log_record".into()))?;
    value.insert("signal".into(), Value::String("logs".into()));
    value.insert(
        "attributes".into(),
        Value::Object(attributes_json(&record.attributes)?),
    );
    value.insert("resource".into(), resource.clone());
    value.insert("scope".into(), scope.clone());
    value.insert(
        "timestamp".into(),
        Value::String(otlp_nanos(record.time_unix_nano)?),
    );
    if !record.event_name.is_empty() {
        value.insert("name".into(), Value::String(record.event_name.clone()));
    } else if let Some(name) = value
        .get("attributes")
        .and_then(Value::as_object)
        .and_then(|attributes| attributes.get("event.name"))
        .and_then(Value::as_str)
    {
        value.insert("name".into(), Value::String(name.to_owned()));
    }
    Ok(Value::Object(value))
}

fn attributes_json(attributes: &[KeyValue]) -> Result<Map<String, Value>, AcquisitionError> {
    let mut values = BTreeMap::new();
    for attribute in attributes {
        if attribute.key.is_empty() || values.contains_key(&attribute.key) {
            return Err(AcquisitionError::Other("invalid_otlp_attribute_key".into()));
        }
        values.insert(
            attribute.key.clone(),
            attribute
                .value
                .as_ref()
                .map(any_value_json)
                .transpose()?
                .unwrap_or(Value::Null),
        );
    }
    Ok(values.into_iter().collect())
}

fn any_value_json(value: &AnyValue) -> Result<Value, AcquisitionError> {
    Ok(match value.value.as_ref() {
        Some(any_value::Value::StringValue(value)) => Value::String(value.clone()),
        Some(any_value::Value::BoolValue(value)) => Value::Bool(*value),
        Some(any_value::Value::IntValue(value)) => Value::Number(Number::from(*value)),
        Some(any_value::Value::DoubleValue(value)) => {
            Number::from_f64(*value).map_or(Value::Null, Value::Number)
        }
        Some(any_value::Value::ArrayValue(value)) => Value::Array(
            value
                .values
                .iter()
                .map(any_value_json)
                .collect::<Result<Vec<_>, _>>()?,
        ),
        Some(any_value::Value::KvlistValue(value)) => {
            Value::Object(attributes_json(&value.values)?)
        }
        Some(any_value::Value::BytesValue(value)) => Value::Array(
            value
                .iter()
                .copied()
                .map(|byte| Value::from(u64::from(byte)))
                .collect(),
        ),
        Some(any_value::Value::StringValueStrindex(value)) => {
            serde_json::json!({ "stringValueStrindex": value })
        }
        None => Value::Null,
    })
}
