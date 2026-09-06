import {
  AddEquation, CustomBlending, Mesh, OneFactor, OneMinusSrcColorFactor,
  OrthographicCamera, PlaneGeometry, Scene, ShaderMaterial, SRGBColorSpace,
  TextureLoader, WebGLRenderer, ZeroFactor,
} from 'three';

export interface TokenSceneController {
  ready: Promise<void>;
  setPaused(paused: boolean): void;
  dispose(): void;
}

const smokeVertexShader = `
  varying vec2 vUv;
  void main() {
    vUv = uv;
    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
  }
`;

const artworkFragmentShader = `
  uniform sampler2D uArtwork;
  varying vec2 vUv;
  void main() {
    gl_FragColor = vec4(texture2D(uArtwork, vUv).rgb, 1.0);
  }
`;

// Advection plus domain-warped noise changes the shape of the smoke itself.
// Two independently evolving layers give the mist depth behind the original logo.
const smokeFragmentShader = `
  uniform float uTime;
  uniform float uFront;
  varying vec2 vUv;
  float hash(vec2 p) {
    p = fract(p * vec2(123.34, 456.21));
    p += dot(p, p + 45.32);
    return fract(p.x * p.y);
  }
  float noise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), f.x),
               mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0)), f.x), f.y);
  }
  float fbm(vec2 p) {
    float value = 0.0;
    float amplitude = 0.52;
    mat2 turn = mat2(0.8, -0.6, 0.6, 0.8);
    for (int i = 0; i < 5; i++) {
      value += amplitude * noise(p);
      p = turn * p * 2.02 + 17.1;
      amplitude *= 0.48;
    }
    return value;
  }
  void main() {
    if (vUv.y < 0.275 || vUv.y > 0.77 || vUv.x < 0.13) discard;
    float time = uTime + uFront * 17.0;
    float rise = vUv.y - 0.325;
    vec2 p = vec2((vUv.x - 0.64) * 4.6, rise * 6.3);
    vec2 drift = vec2(-time * 0.09, -time * 0.22);
    vec2 curl = vec2(fbm(p * 1.25 + drift), fbm(p * 1.35 + drift + 8.2));
    float billow = fbm(p * 2.1 + curl * 3.3 + drift);
    float wisps = smoothstep(0.36, 0.73, billow);
    float body = smoothstep(0.25, 0.67, fbm(p * 0.85 + drift * 0.55));
    float centre = 0.62 + rise * 0.35 + sin(time * 0.19 + rise * 9.0) * 0.035;
    float spread = 0.35 + max(rise, 0.0) * 0.2;
    float envelope = exp(-pow((vUv.x - centre) / spread, 2.0) * 1.8);
    envelope *= smoothstep(0.28, 0.335, vUv.y);
    envelope *= 1.0 - smoothstep(0.43 - uFront * 0.035, 0.75 - uFront * 0.23, vUv.y);
    float density = (wisps * 0.72 + body * 0.28) * envelope;
    float alpha = density * mix(0.58, 0.28, uFront);
    vec3 colour = mix(vec3(0.29, 0.40, 0.15), vec3(0.68, 0.76, 0.43), wisps);
    gl_FragColor = vec4(colour, alpha);
  }
`;

export function createTokenScene(canvas: HTMLCanvasElement): TokenSceneController {
  const renderer = new WebGLRenderer({ canvas, alpha: true, antialias: true, powerPreference: 'low-power' });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.25));
  renderer.setClearColor(0x000000, 0);
  renderer.outputColorSpace = SRGBColorSpace;

  const scene = new Scene();
  const camera = new OrthographicCamera(0, 1254, 1254, 0, .1, 5000);
  camera.position.z = 2000;
  let disposed = false;
  let assetsLoaded = 0;
  let resolveReady!: () => void;
  let rejectReady!: (reason: unknown) => void;
  const ready = new Promise<void>((resolve, reject) => { resolveReady = resolve; rejectReady = reject; });
  const loader = new TextureLoader();
  const onAssetLoaded = () => {
    if (disposed) { resolveReady(); return; }
    if (++assetsLoaded === 2) {
      resize();
      resolveReady();
    }
  };
  const landscape = loader.load(`${import.meta.env.BASE_URL}images/auth-landscape.webp`, onAssetLoaded, undefined, rejectReady);
  const originalLogo = loader.load(`${import.meta.env.BASE_URL}images/auth-ribbon-foreground.webp`, onAssetLoaded, undefined, rejectReady);
  const smokeGeometry = new PlaneGeometry(1254, 1254);
  const landscapeMaterial = new ShaderMaterial({
    uniforms: { uArtwork: { value: landscape } },
    vertexShader: smokeVertexShader, fragmentShader: artworkFragmentShader, toneMapped: false,
  });
  const backdrop = new Mesh(smokeGeometry, landscapeMaterial);
  backdrop.position.set(627, 627, -800);
  scene.add(backdrop);

  // Render the original ribbon directly, keeping its surface smooth while
  // the surrounding smoke animates independently.
  const envelopeMaterial = new ShaderMaterial({
    uniforms: { uArtwork: { value: originalLogo } },
    vertexShader: smokeVertexShader, fragmentShader: artworkFragmentShader,
    transparent: true, depthWrite: false, depthTest: false, toneMapped: false,
    blending: CustomBlending, blendEquation: AddEquation,
    blendSrc: OneFactor, blendDst: OneMinusSrcColorFactor,
    blendSrcAlpha: ZeroFactor, blendDstAlpha: OneFactor,
  });
  const envelope = new Mesh(smokeGeometry, envelopeMaterial);
  envelope.position.set(627, 627, -799);
  envelope.renderOrder = 1;
  scene.add(envelope);
  const smokeMaterials = [0, 1].map((front) => new ShaderMaterial({
    uniforms: { uTime: { value: 0 }, uFront: { value: front } },
    vertexShader: smokeVertexShader,
    fragmentShader: smokeFragmentShader,
    transparent: true, depthWrite: false, depthTest: false, toneMapped: false,
  }));
  smokeMaterials.forEach((material, index) => {
    const smoke = new Mesh(smokeGeometry, material);
    smoke.position.set(627, 627, index ? 400 : -400);
    smoke.renderOrder = index ? 3 : 0;
    scene.add(smoke);
  });

  let paused = false;
  let frameId = 0;
  let lastFrame = 0;
  let elapsed = 0;
  let lastDiagnostic = -1;
  const draw = () => {
    if (assetsLoaded < 2 || disposed) return;
    smokeMaterials.forEach((material) => { material.uniforms.uTime.value = elapsed; });
    renderer.render(scene, camera);
    if (import.meta.env.DEV && Math.floor(elapsed * 2) !== lastDiagnostic) {
      lastDiagnostic = Math.floor(elapsed * 2);
      canvas.dataset.sceneTime = elapsed.toFixed(2);
      canvas.dataset.triangles = String(renderer.info.render.triangles);
    }
  };
  const resize = () => {
    if (disposed) return;
    const { width, height } = canvas.getBoundingClientRect();
    if (!width || !height) return;
    renderer.setSize(width, height, false);
    // Match the static artwork, keeping the logo in view in tall panels too.
    const side = Math.max(width, height);
    const visibleHeight = 1254 * height / side;
    const visibleWidth = 1254 * width / side;
    camera.left = (1254 - visibleWidth) * .7;
    camera.right = camera.left + visibleWidth;
    camera.top = (1254 + visibleHeight) / 2;
    camera.bottom = (1254 - visibleHeight) / 2;
    camera.updateProjectionMatrix();
    draw();
  };
  const frame = (now: number) => {
    if (paused || disposed) return;
    frameId = requestAnimationFrame(frame);
    if (assetsLoaded < 2) return;
    if (lastFrame && now - lastFrame < 1000 / 30) return;
    if (lastFrame) elapsed += Math.min((now - lastFrame) / 1000, .1);
    lastFrame = now;
    draw();
  };
  const observer = new ResizeObserver(resize);
  observer.observe(canvas);
  resize();
  frameId = requestAnimationFrame(frame);

  return {
    ready,
    setPaused(value) {
      if (disposed || paused === value) return;
      paused = value;
      cancelAnimationFrame(frameId);
      lastFrame = 0;
      if (!paused) frameId = requestAnimationFrame(frame);
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      cancelAnimationFrame(frameId);
      observer.disconnect();
      smokeGeometry.dispose();
      smokeMaterials.forEach((material) => material.dispose());
      envelopeMaterial.dispose();
      landscapeMaterial.dispose();
      landscape.dispose();
      originalLogo.dispose();
      resolveReady();
      renderer.dispose();
    },
  };
}
