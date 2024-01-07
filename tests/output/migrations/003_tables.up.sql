CREATE TABLE "machines" (
	"toys_produced" bigint NOT NULL,
	"name" text COLLATE "pg_catalog"."default" NOT NULL
);
CREATE TABLE "storage_locations" (
	"shelf" bigint NOT NULL,
	"total_capacity" bigint NOT NULL,
	"used_capacity" bigint NOT NULL,
	"current_toy_type" text COLLATE "pg_catalog"."default" NOT NULL
);