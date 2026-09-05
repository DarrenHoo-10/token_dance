import React from 'react';

export function TokenOrbit() {
  return (
    <div className="token-orbit" aria-hidden="true">
      <div className="token-orbit__halo" />
      <div className="token-orbit__plane">
        <div className="token-orbit__track token-orbit__track--outer" />
        <div className="token-orbit__track token-orbit__track--inner" />
        <div className="token-orbit__ring">
          {Array.from({ length: 40 }, (_, i) => (
            <i key={i} style={{ '--segment': i } as React.CSSProperties} />
          ))}
        </div>
        <div className="token-orbit__satellite"><span /></div>
        <div className="token-orbit__satellite token-orbit__satellite--second"><span /></div>
      </div>
      <div className="token-orbit__core"><img src="/logo-tokendance-v2.png" alt="" /></div>
      <span className="token-orbit__code token-orbit__code--one">{'{ }'}</span>
      <span className="token-orbit__code token-orbit__code--two">{'</>'}</span>
      <span className="token-orbit__code token-orbit__code--three">{'✳'}</span>
    </div>
  );
}

export type CompanionMood = 'idle' | 'email' | 'password' | 'loading' | 'error';

export function LoginCompanions({ mood, caption }: { mood: CompanionMood; caption: string }) {
  return (
    <div className="login-companions" data-mood={mood} aria-hidden="true">
      <span className="login-companions__caption">{caption}</span>
      <div className="login-companions__friends">
        {['lime', 'cream'].map((color) => (
          <div className={`login-friend login-friend--${color}`} key={color}>
            <span className="login-friend__tuft" />
            <div className="login-friend__face">
              <span className="login-friend__eye" /><span className="login-friend__eye" />
              <span className="login-friend__cheek login-friend__cheek--left" />
              <span className="login-friend__cheek login-friend__cheek--right" />
              <span className="login-friend__mouth" />
            </div>
            <span className="login-friend__hand login-friend__hand--left" />
            <span className="login-friend__hand login-friend__hand--right" />
          </div>
        ))}
      </div>
      <span className="login-companions__ground" />
    </div>
  );
}
