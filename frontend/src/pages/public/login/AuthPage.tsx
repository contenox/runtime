import { Card } from '@cate/ui';
import { useState } from 'react';
import { LoginForm } from './LoginForm';
import { RegisterForm } from './RegisterForm';

export default function AuthPage() {
  const [isRegistering, setIsRegistering] = useState(false);

  return (
    <Card>
      {isRegistering ? (
        <RegisterForm onSwitch={() => setIsRegistering(false)} />
      ) : (
        <LoginForm onSwitch={() => setIsRegistering(true)} />
      )}
    </Card>
  );
}
