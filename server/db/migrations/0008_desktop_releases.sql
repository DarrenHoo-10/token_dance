CREATE TABLE desktop_releases (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
 platform VARCHAR(32) NOT NULL,
 version VARCHAR(64) NOT NULL,
 source_branch VARCHAR(32) NOT NULL,
 source_commit CHAR(40) NOT NULL,
 manifest_json JSON NOT NULL,
 created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY (id),
 UNIQUE KEY uq_desktop_release_version (platform, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE desktop_release_channels (
 platform VARCHAR(32) NOT NULL,
 release_id BIGINT UNSIGNED NOT NULL,
 updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
 PRIMARY KEY (platform),
 CONSTRAINT fk_desktop_channel_release FOREIGN KEY (release_id) REFERENCES desktop_releases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE desktop_release_publication (
 id TINYINT UNSIGNED NOT NULL,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
 rendered_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
 rendered_at DATETIME(3) NULL,
 PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
