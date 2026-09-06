import { useId } from 'react';

/** Scalable glass and light layers; quota geometry is rendered separately. */
export function OrbMaterial() {
  const id = useId().replace(/:/g, '');
  const paint = (name: string) => `url(#${id}-${name})`;
  return <svg className="orb-material" viewBox="0 0 100 100" aria-hidden="true">
    <defs>
      <radialGradient id={`${id}-body`} cx="48%" cy="34%" r="68%">
        <stop stopColor="#162823" /><stop offset=".58" stopColor="#071210" />
        <stop offset=".86" stopColor="#10231a" /><stop offset="1" stopColor="var(--orb-color)" />
      </radialGradient>
      <linearGradient id={`${id}-glass`} x1="0" y1="0" x2=".18" y2="1">
        <stop stopColor="#fff" /><stop offset=".12" stopColor="#fff" stopOpacity=".95" />
        <stop offset=".35" stopColor="#edf6f1" stopOpacity=".62" />
        <stop offset=".75" stopColor="#dce9e5" stopOpacity=".12" /><stop offset="1" stopColor="#fff" stopOpacity="0" />
      </linearGradient>
      <linearGradient id={`${id}-bevel`} x2=".8" y2="1">
        <stop stopColor="#d6e3d9" /><stop offset=".25" stopColor="#40564b" />
        <stop offset=".55" stopColor="#15271c" /><stop offset=".84" stopColor="var(--orb-color)" /><stop offset="1" stopColor="#efffc7" />
      </linearGradient>
      <radialGradient id={`${id}-energy`} cx="55%" cy="100%" r="65%">
        <stop stopColor="var(--orb-color)" stopOpacity=".9" /><stop offset=".36" stopColor="var(--orb-color)" stopOpacity=".36" />
        <stop offset="1" stopColor="var(--orb-color)" stopOpacity="0" />
      </radialGradient>
      <linearGradient id={`${id}-filament`} x1="0" y1="0" x2="1" y2=".4">
        <stop stopColor="var(--orb-color)" stopOpacity="0" /><stop offset=".32" stopColor="var(--orb-color)" stopOpacity=".7" />
        <stop offset=".65" stopColor="#f4ffd8" /><stop offset=".82" stopColor="var(--orb-color)" /><stop offset="1" stopColor="var(--orb-color)" stopOpacity="0" />
      </linearGradient>
      <radialGradient id={`${id}-spark`}>
        <stop stopColor="#fff" /><stop offset=".15" stopColor="#faffd9" /><stop offset=".4" stopColor="var(--orb-color)" stopOpacity=".85" /><stop offset="1" stopColor="var(--orb-color)" stopOpacity="0" />
      </radialGradient>
      <clipPath id={`${id}-clip`}><circle cx="50" cy="50" r="43" /></clipPath>
    </defs>
    <circle cx="50" cy="50" r="44.4" fill="#122019" stroke={paint('bevel')} strokeWidth="1.4" />
    <g clipPath={paint('clip')}>
      <circle cx="50" cy="50" r="43" fill={paint('body')} />
      <circle cx="50" cy="50" r="43" fill={paint('energy')} />
      <path d="M10 43 C8 20 29 5 50 7 C71 6 86 19 87 32 C76 24 64 19 47 26 C30 30 22 37 13 57 Z" fill={paint('glass')} />
      <ellipse cx="24" cy="45" rx="7" ry="22" fill="#d9e9df" opacity=".055" transform="rotate(20 24 45)" />
      <g className="orb-filaments" fill="none" stroke={paint('filament')} strokeWidth=".45">
        <ellipse cx="51" cy="69" rx="40" ry="20" transform="rotate(-30 51 69)" />
        <ellipse cx="49" cy="70" rx="39" ry="19" transform="rotate(24 49 70)" />
        <path d="M12 53 C19 89 57 103 79 80 C88 70 85 54 92 43" strokeWidth=".65" />
        <path d="M14 65 C29 91 56 83 84 69 M20 83 C48 95 80 76 87 58" />
      </g>
      <path d="M18 71 A37 37 0 0 0 85 61" fill="none" stroke="var(--orb-color)" strokeWidth="1.5" opacity=".45" />
      <circle cx="73" cy="80" r="4.6" fill={paint('spark')} />
      <circle cx="19" cy="67" r="2.7" fill={paint('spark')} />
      <circle cx="77" cy="25" r="4" fill={paint('spark')} />
      <circle cx="34" cy="86" r=".5" fill="#f4ffd8" /><circle cx="83" cy="58" r=".4" fill="#f4ffd8" />
    </g>
    <circle cx="50" cy="50" r="42.5" fill="none" stroke={paint('bevel')} strokeWidth=".6" opacity=".85" />
  </svg>;
}
