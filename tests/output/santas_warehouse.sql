-- Database generated with pgModeler (PostgreSQL Database Modeler).
-- pgModeler version: 1.0.6
-- PostgreSQL version: 16.0
-- Project Site: pgmodeler.io
-- Model Author: ---
-- -- object: santa | type: ROLE --
-- -- DROP ROLE IF EXISTS santa;
-- CREATE ROLE santa WITH ;
-- -- ddl-end --
-- 
-- -- object: worker | type: ROLE --
-- -- DROP ROLE IF EXISTS worker;
-- CREATE ROLE worker WITH ;
-- -- ddl-end --
-- 

-- Database creation must be performed outside a multi lined SQL file. 
-- These commands were put in this file only as a convenience.
-- 
-- -- object: north_pole | type: DATABASE --
-- -- DROP DATABASE IF EXISTS north_pole;
-- CREATE DATABASE north_pole;
-- -- ddl-end --
-- 

SET check_function_bodies = false;
-- ddl-end --

-- object: warehouse | type: SCHEMA --
-- DROP SCHEMA IF EXISTS warehouse CASCADE;
CREATE SCHEMA warehouse;
-- ddl-end --
ALTER SCHEMA warehouse OWNER TO postgres;
-- ddl-end --

-- object: factory | type: SCHEMA --
-- DROP SCHEMA IF EXISTS factory CASCADE;
CREATE SCHEMA factory;
-- ddl-end --
ALTER SCHEMA factory OWNER TO postgres;
-- ddl-end --

SET search_path TO pg_catalog,public,warehouse,factory;
-- ddl-end --

-- object: public.seq_storage_locations_id | type: SEQUENCE --
-- DROP SEQUENCE IF EXISTS public.seq_storage_locations_id CASCADE;
CREATE SEQUENCE public.seq_storage_locations_id
	INCREMENT BY 1
	MINVALUE 0
	MAXVALUE 2147483647
	START WITH 1
	CACHE 1
	NO CYCLE
	OWNED BY NONE;

-- ddl-end --
ALTER SEQUENCE public.seq_storage_locations_id OWNER TO postgres;
-- ddl-end --

-- object: public.seq_machines_id | type: SEQUENCE --
-- DROP SEQUENCE IF EXISTS public.seq_machines_id CASCADE;
CREATE SEQUENCE public.seq_machines_id
	INCREMENT BY 1
	MINVALUE 0
	MAXVALUE 2147483647
	START WITH 1
	CACHE 1
	NO CYCLE
	OWNED BY NONE;

-- ddl-end --
ALTER SEQUENCE public.seq_machines_id OWNER TO postgres;
-- ddl-end --

-- object: public.machines | type: TABLE --
-- DROP TABLE IF EXISTS public.machines CASCADE;
CREATE TABLE public.machines (
	id bigint NOT NULL DEFAULT nextval('public.seq_machines_id'::regclass),
	name text NOT NULL,
	toys_produced bigint NOT NULL,
	CONSTRAINT machines_pk PRIMARY KEY (id)
);
-- ddl-end --
ALTER TABLE public.machines OWNER TO postgres;
-- ddl-end --

-- object: public.storage_locations | type: TABLE --
-- DROP TABLE IF EXISTS public.storage_locations CASCADE;
CREATE TABLE public.storage_locations (
	id bigint NOT NULL DEFAULT nextval('public.seq_storage_locations_id'::regclass),
	shelf bigint NOT NULL,
	total_capacity bigint NOT NULL,
	used_capacity bigint NOT NULL,
	current_toy_type text NOT NULL,
	CONSTRAINT storage_locations_pk PRIMARY KEY (id),
	CONSTRAINT ck_capacity CHECK (total_capacity >= used_capacity)
);
-- ddl-end --
ALTER TABLE public.storage_locations OWNER TO postgres;
-- ddl-end --

-- object: public.tr_machines_toys_produced_increase | type: FUNCTION --
-- DROP FUNCTION IF EXISTS public.tr_machines_toys_produced_increase() CASCADE;
CREATE FUNCTION public.tr_machines_toys_produced_increase ()
	RETURNS trigger
	LANGUAGE plpgsql
	VOLATILE 
	CALLED ON NULL INPUT
	SECURITY INVOKER
	PARALLEL UNSAFE
	COST 1
	AS $$
BEGIN
	IF NEW.toys_produced < OLD.toys_produced THEN
		RAISE EXCEPTION 'Toys produced count can not be lowered';
	END IF;
END;
$$;
-- ddl-end --
ALTER FUNCTION public.tr_machines_toys_produced_increase() OWNER TO postgres;
-- ddl-end --

-- object: toys_produced_increase | type: TRIGGER --
-- DROP TRIGGER IF EXISTS toys_produced_increase ON public.machines CASCADE;
CREATE TRIGGER toys_produced_increase
	BEFORE UPDATE OF toys_produced
	ON public.machines
	FOR EACH ROW
	EXECUTE PROCEDURE public.tr_machines_toys_produced_increase();
-- ddl-end --


