CREATE TABLE certificates (
  serial_number            bytea NOT NULL,
  authority_key_identifier bytea NOT NULL,
  ca_label                 bytea,
  status                   bytea NOT NULL,
  reason                   int,
  expiry                   timestamptz,
  revoked_at               timestamptz,
  pem                      bytea NOT NULL,
  issued_at                timestamptz,
  not_before               timestamptz,
  metadata                 jsonb,
  sans                     text[] NULL,
  common_name              text,
  PRIMARY KEY(serial_number, authority_key_identifier)
);

CREATE TABLE ocsp_responses (
  serial_number            bytea NOT NULL,
  authority_key_identifier bytea NOT NULL,
  body                     bytea NOT NULL,
  expiry                   timestamptz,
  PRIMARY KEY(serial_number, authority_key_identifier),
  FOREIGN KEY(serial_number, authority_key_identifier) REFERENCES certificates(serial_number, authority_key_identifier)
);

CREATE INDEX certificates_serial_idx ON certificates (serial_number);
CREATE INDEX certificates_aki_idx ON certificates (authority_key_identifier);
CREATE INDEX certificates_expiry_idx ON certificates (expiry);
CREATE INDEX certificates_ca_label_idx ON certificates (ca_label);
CREATE INDEX certificates_common_name_idx ON certificates (common_name);
CREATE INDEX ocsp_responses_serial_idx ON ocsp_responses (serial_number);
CREATE INDEX ocsp_responses_aki_idx ON ocsp_responses (authority_key_identifier);
CREATE INDEX ocsp_responses_expiry_idx ON ocsp_responses (expiry);