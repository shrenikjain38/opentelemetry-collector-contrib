# Filebeat Receiver

| Status                   |           |
| ------------------------ |-----------|
| Stability                | [alpha]   |
| Supported pipeline types | logs      |
| Distributions            | [contrib] |

The Filebeat receiver accepts the logs from [filebeat](https://www.elastic.co/beats/filebeat) agent which transports the logs as filebeat batch containing
filebeat events over [logstash](https://www.elastic.co/guide/en/beats/filebeat/current/logstash-output.html) output. 


## Configuration

### Default

By default, without any configuration, the receiver will listen on `http://localhost:5044/`. The TLS settings will be empty.

```yaml
receivers:
  filebeat:
```

### Customising

The following can be configured:
- endpoint: Configure the emdpoint 
- tls : Takes values of relevant TLS settings

### Example configuration

```yaml
receivers:
  filebeat:
    endpoint: "0.0.0.0:5044"
    tls:
      ca_file: "/abc"
      cert_file: "/abcd"
      key_file: "/abcde"
      min_version: "abcdef"
      max_version: "abcdefg"
      reload_interval: 20
```

[alpha]:https://github.com/open-telemetry/opentelemetry-collector#alpha
[contrib]:https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib