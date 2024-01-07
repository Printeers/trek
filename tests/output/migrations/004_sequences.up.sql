CREATE SEQUENCE "public"."seq_machines_id"
	AS bigint
	INCREMENT BY 1
	MINVALUE 0 MAXVALUE 2147483647
	START WITH 1 CACHE 1 NO CYCLE
;
CREATE SEQUENCE "public"."seq_storage_locations_id"
	AS bigint
	INCREMENT BY 1
	MINVALUE 0 MAXVALUE 2147483647
	START WITH 1 CACHE 1 NO CYCLE
;
ALTER TABLE "public"."machines" ADD COLUMN "id" bigint NOT NULL DEFAULT nextval('seq_machines_id'::regclass);
CREATE UNIQUE INDEX CONCURRENTLY machines_pk ON public.machines USING btree (id);
ALTER TABLE "public"."machines" ADD CONSTRAINT "machines_pk" PRIMARY KEY USING INDEX "machines_pk";
ALTER TABLE "public"."storage_locations" ADD COLUMN "id" bigint NOT NULL DEFAULT nextval('seq_storage_locations_id'::regclass);
CREATE UNIQUE INDEX CONCURRENTLY storage_locations_pk ON public.storage_locations USING btree (id);
ALTER TABLE "public"."storage_locations" ADD CONSTRAINT "storage_locations_pk" PRIMARY KEY USING INDEX "storage_locations_pk";