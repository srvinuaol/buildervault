# TSM Audit Server

An example values file for deploying the Builder Vault audit server.

For instructions on how to generate the keys needed here, go to the Builder Vault [documentation](https://builder-vault-tsm.docs.blockdaemon.com/docs/audit-server).

```
mongodb:
  enabled: true
  useStatefulSet: true
  auth:
    rootPassword: "<secret>"
  disableJavascript: true
config:
  configFile: |
    [Database]
    Host = "tsm-audit-mongodb:27017"
    Username = "root"
    Password = "<secret>"

    [LogServer]
    Port = 3000
    PrivateKey = "<audit server private key>

    [QueryServer]
    Port = 8080
    CertificateFile = ""      #HTTPS disabled
    CertificateKeyFile = "" # HTTP disabled

    [TSM.demo]
    Password = "<password>"
    PublicKeys = [
      "<node0 public key>",
      "<node1 public key>",
    ]

image:
  repository: <image repo>
  pullPolicy: Always
  # Overrides the image tag whose default is the chart appVersion.
  tag: "v1.1.0"

service:
  type: NodePort

ingress:
  enabled: true
  className: "alb"
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/certificate-arn: <acm cert arn>
    alb.ingress.kubernetes.io/healthcheck-path: /ping
    alb.ingress.kubernetes.io/success-codes: "204"
  hosts:
    - host: "tsm-audit.example.com"
      paths:
        - path: /
          pathType: Prefix
          port: 8080

```