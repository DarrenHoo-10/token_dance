# Registration avatar catalog

Four pregenerated avatars: cat, fox, panda, bunny. Generated with the built-in
image generation tool on 2026-09-06, one independent request per animal.
These are static assets; registration does not call an image service.

Prompt used for each image (replace `{animal}` with its catalog ID):

> Generate ONE ready-to-use square profile avatar image for TokenDance, a friendly coding community. Subject: one adorable {animal}, head and shoulders, centered large, front-facing, charming simple expressive face. Premium matte soft 3D clay illustration, rounded shapes, tiny black eyes, subtle studio shadows, warm cream solid background, pastel sage and lime green accent scarf. Clean iconic silhouette legible at 40px. No text, letters, logos, border, watermark, extra characters or collage. Square 1:1 composition with safe 12 percent margins. Cohesive minimal cute designer toy aesthetic.

Keep IDs synchronized with `web/src/pages/auth/registrationProfile.ts` and
`server/internal/auth/registration_profile.go`. Existing accounts are unchanged.
Custom avatar uploads remain available in profile settings after registration.
