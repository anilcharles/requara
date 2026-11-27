Put SAML files here (they're mounted read-only at /etc/requara/saml):

- the identity provider's metadata XML (`REQUARA_AUTH_<ID>_METADATA_FILE`), or its
  signing certificate (`REQUARA_AUTH_<ID>_IDP_CERT_FILE`),
- optionally Requara's own certificate and key to sign requests and decrypt
  assertions (`REQUARA_AUTH_<ID>_SP_CERT_FILE`, `_SP_KEY_FILE`).

Not needed when you use `REQUARA_AUTH_<ID>_METADATA_URL`.
