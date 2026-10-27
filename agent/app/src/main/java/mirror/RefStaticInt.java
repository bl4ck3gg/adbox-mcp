package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefStaticInt {
    private Field field;

    public RefStaticInt(Class<?> cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public int get() {
        try {
            return this.field.getInt(null);
        } catch (Exception e) {
            return 0;
        }
    }

    public void set(int value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setInt(null, value);
        } catch (Exception e) {
        }
    }
}
