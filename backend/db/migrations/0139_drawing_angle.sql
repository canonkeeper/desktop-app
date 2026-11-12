-- The camera of a version of «Сменить ракурс» (specs/2026-11-11-drawing-angle.md DA2.3): where it stands relative to the
-- frame of its base — degrees around the scene, positive to the right; degrees above the point of the base; the distance
-- as a part of that of the base. The version keeps it where it keeps its origin, beside cutout and enlarged; the three are
-- NULL together on any other drawing.
ALTER TABLE public.saga_images
    ADD COLUMN angle_azimuth double precision,
    ADD COLUMN angle_elevation double precision,
    ADD COLUMN angle_distance double precision,
    ADD CONSTRAINT saga_images_angle_check CHECK (
        (angle_azimuth IS NULL AND angle_elevation IS NULL AND angle_distance IS NULL)
        OR (angle_azimuth BETWEEN -180 AND 180 AND angle_elevation BETWEEN -20 AND 80 AND angle_distance BETWEEN 0.5 AND 2)
    );

REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;
