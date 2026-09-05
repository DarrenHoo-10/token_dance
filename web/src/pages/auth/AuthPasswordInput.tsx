import { Eye, EyeOff } from 'lucide-react';
import { Input, type InputProps } from '@/components/common/Input';
import { useLocale } from '@/context/LocaleContext';

interface AuthPasswordInputProps extends Omit<InputProps, 'type' | 'suffix'> {
  visible: boolean;
  onToggleVisibility: () => void;
}

export function AuthPasswordInput({ visible, onToggleVisibility, ...props }: AuthPasswordInputProps) {
  const { t } = useLocale();

  return (
    <Input
      {...props}
      type={visible ? 'text' : 'password'}
      suffix={(
        <button
          type="button"
          className="login-password-toggle"
          aria-label={t(visible ? 'auth.hidePassword' : 'auth.showPassword')}
          aria-pressed={visible}
          onClick={onToggleVisibility}
        >
          {visible ? <EyeOff size={17} aria-hidden="true" /> : <Eye size={17} aria-hidden="true" />}
        </button>
      )}
    />
  );
}
